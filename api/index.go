package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

var (
	mongoClient  *mongo.Client
	henrikAPIKey string
	httpClient   *http.Client
	router       *gin.Engine // Gin 엔진을 전역으로 선언
)

func init() {
	// 1. MongoDB 초기화
	mongoURI := os.Getenv("MONGO_URI")
	if mongoURI == "" {
		log.Println("경고: MONGO_URI가 설정되지 않았습니다.")
	} else {
		clientOptions := options.Client().ApplyURI(mongoURI)
		var err error
		mongoClient, err = mongo.Connect(context.Background(), clientOptions)
		if err != nil {
			log.Printf("MongoDB 연결 실패: %v", err)
		}
	}

	// 2. HenrikDev API 및 HTTP 클라이언트 설정
	henrikAPIKey = os.Getenv("HENRIK_API_KEY")
	httpClient = &http.Client{Timeout: 15 * time.Second}

	// 3. Gin 라우터 설정
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	// 라우트 등록
	r.GET("/api/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": "pong"})
	})

	r.GET("/api/account/riotid", getAccount)
	r.GET("/api/player/matches/:puuid", getMatches)

	router = r
}

// Vercel이 호출하는 실제 핸들러 함수 (표준 http.HandlerFunc 시그니처)
func Handler(w http.ResponseWriter, r *http.Request) {
	// 모든 요청을 Gin 라우터로 전달
	router.ServeHTTP(w, r)
}

// --- 핸들러 함수들 ---

func getAccount(c *gin.Context) {
	name := c.Query("gameName")
	tag := c.Query("tagLine")

	if name == "" || tag == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "gameName과 tagLine이 필요합니다."})
		return
	}

	col := mongoClient.Database("valorant").Collection("playerAccounts")
	var result map[string]interface{}
	if err := col.FindOne(context.Background(), bson.M{"name": name, "tag": tag}).Decode(&result); err == nil {
		c.JSON(http.StatusOK, result)
		return
	}

	url := fmt.Sprintf("https://api.henrikdev.xyz/valorant/v1/account/%s/%s", name, tag)
	req, _ := http.NewRequest("GET", url, nil)
	if henrikAPIKey != "" { req.Header.Add("Authorization", henrikAPIKey) }

	resp, err := httpClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "API 호출 실패"})
		return
	}
	defer resp.Body.Close()

	var apiRes map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&apiRes)
	userData := apiRes["data"].(map[string]interface{})

	col.InsertOne(context.Background(), userData)
	c.JSON(http.StatusOK, userData)
}

func getMatches(c *gin.Context) {
	puuid := c.Param("puuid")
	region := c.DefaultQuery("region", "kr")

	url := fmt.Sprintf("https://api.henrikdev.xyz/valorant/v3/by-puuid/matches/%s/%s", region, puuid)
	req, _ := http.NewRequest("GET", url, nil)
	if henrikAPIKey != "" { req.Header.Add("Authorization", henrikAPIKey) }

	resp, err := httpClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "경기 데이터 로드 실패"})
		return
	}
	defer resp.Body.Close()

	var res struct { Data []map[string]interface{} `json:"data"` }
	json.NewDecoder(resp.Body).Decode(&res)

	findings := CheckForAbusing(res.Data, puuid)

	c.JSON(http.StatusOK, gin.H{
		"matchesCount": len(res.Data),
		"abusingDetected": len(findings) > 0,
		"details": findings,
	})
}

func CheckForAbusing(matches []map[string]interface{}, targetPUUID string) []string {
	var findings []string
	type stats struct { met int; lost int }
	opponents := make(map[string]*stats)

	for _, m := range matches {
		playersData, ok := m["players"].(map[string]interface{})
		if !ok { continue }
		allPlayers, ok := playersData["all_players"].([]interface{})
		if !ok { continue }

		var targetTeam string
		for _, p := range allPlayers {
			player := p.(map[string]interface{})
			if player["puuid"] == targetPUUID {
				targetTeam = player["team"].(string)
				break
			}
		}

		teams := m["teams"].(map[string]interface{})
		teamKey := "red"; if targetTeam == "Blue" { teamKey = "blue" }
		teamInfo, ok := teams[teamKey].(map[string]interface{})
		if !ok { continue }
		won := teamInfo["has_won"].(bool)

		for _, p := range allPlayers {
			opp := p.(map[string]interface{})
			if opp["puuid"] == targetPUUID || opp["team"] == targetTeam { continue }
			
			id := opp["puuid"].(string)
			if _, ok := opponents[id]; !ok { opponents[id] = &stats{} }
			opponents[id].met++
			if !won { opponents[id].lost++ }
		}
	}

	for id, s := range opponents {
		if s.met >= 5 && float64(s.lost)/float64(s.met) >= 0.8 {
			findings = append(findings, fmt.Sprintf("상대 %s와 %d번 만나 %d번 패배 (어뷰징 의심)", id, s.met, s.lost))
		}
	}
	return findings
}