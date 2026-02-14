package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
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
	router       *gin.Engine
)

func init() {
	mongoURI := os.Getenv("MONGO_URI")
	if mongoURI != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		clientOptions := options.Client().ApplyURI(mongoURI)
		client, err := mongo.Connect(ctx, clientOptions)
		if err == nil {
			mongoClient = client
		} else {
			log.Printf("MongoDB 연결 초기 실패: %v", err)
		}
	}

	henrikAPIKey = os.Getenv("HENRIK_API_KEY")
	httpClient = &http.Client{Timeout: 15 * time.Second}

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery()) // 패닉 발생 시 서버 종료 대신 500 에러 반환

	r.GET("/api/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": "pong"})
	})

	r.GET("/api/account/riotid", getAccount)
	r.GET("/api/player/matches/:puuid", getMatches)

	router = r
}

func Handler(w http.ResponseWriter, r *http.Request) {
	router.ServeHTTP(w, r)
}

func getAccount(c *gin.Context) {
	name := c.Query("gameName")
	tag := c.Query("tagLine")

	if name == "" || tag == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "gameName과 tagLine이 필요합니다."})
		return
	}

	// mongoClient가 nil인지 확인
	if mongoClient == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "데이터베이스 연결 안 됨"})
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": "라이엇 계정 정보를 가져오지 못했습니다."})
		return
	}
	defer resp.Body.Close()

	var apiRes map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&apiRes); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "응답 파싱 실패"})
		return
	}
	
	userData, ok := apiRes["data"].(map[string]interface{})
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "플레이어를 찾을 수 없습니다."})
		return
	}

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
		c.JSON(http.StatusInternalServerError, gin.H{"error": "경기 데이터를 가져오지 못했습니다."})
		return
	}
	defer resp.Body.Close()

	var res struct { Data []map[string]interface{} `json:"data"` }
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "경기 데이터 파싱 실패"})
		return
	}

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
			player, ok := p.(map[string]interface{})
			if !ok { continue }
			if p_puuid, ok := player["puuid"].(string); ok && p_puuid == targetPUUID {
				targetTeam, _ = player["team"].(string)
				break
			}
		}

		teams, ok := m["teams"].(map[string]interface{})
		if !ok { continue }
		teamKey := "red"
		if targetTeam == "Blue" { teamKey = "blue" }
		
		teamInfo, ok := teams[teamKey].(map[string]interface{})
		if !ok { continue }
		
		won, _ := teamInfo["has_won"].(bool)

		for _, p := range allPlayers {
			opp, ok := p.(map[string]interface{})
			if !ok { continue }
			oppPUUID, _ := opp["puuid"].(string)
			oppTeam, _ := opp["team"].(string)

			if oppPUUID == targetPUUID || oppTeam == targetTeam { continue }
			
			if _, ok := opponents[oppPUUID]; !ok { opponents[oppPUUID] = &stats{} }
			opponents[oppPUUID].met++
			if !won { opponents[oppPUUID].lost++ }
		}
	}

	for id, s := range opponents {
		if s.met >= 5 && float64(s.lost)/float64(s.met) >= 0.8 {
			findings = append(findings, fmt.Sprintf("상대 %s와 %d번 만나 %d번 패배 (어뷰징 의심)", id, s.met, s.lost))
		}
	}
	return findings
}