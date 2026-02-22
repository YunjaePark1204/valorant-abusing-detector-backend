package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
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
		if client, err := mongo.Connect(ctx, clientOptions); err == nil {
			mongoClient = client
		}
	}

	henrikAPIKey = os.Getenv("HENRIK_API_KEY")
	httpClient = &http.Client{Timeout: 15 * time.Second}

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	r.GET("/api/ping", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"message": "pong"}) })
	r.GET("/api/account/riotid", getAccount)
	r.GET("/api/player/matches/:puuid", getMatches)

	router = r
}

func Handler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "https://valorant-abusing-frontend.vercel.app")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS, PUT, DELETE")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	w.Header().Set("Access-Control-Allow-Credentials", "true")

	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}
	router.ServeHTTP(w, r)
}

func getAccount(c *gin.Context) {
	name, tag := c.Query("gameName"), c.Query("tagLine")
	if name == "" || tag == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "gameName과 tagLine이 필요합니다."})
		return
	}

	if mongoClient != nil {
		col := mongoClient.Database("valorant").Collection("playerAccounts")
		var result map[string]interface{}
		if err := col.FindOne(context.Background(), bson.M{"name": name, "tag": tag}).Decode(&result); err == nil {
			c.JSON(http.StatusOK, result)
			return
		}
	}

	url := fmt.Sprintf("https://api.henrikdev.xyz/valorant/v1/account/%s/%s", name, tag)
	req, _ := http.NewRequest("GET", url, nil)
	if henrikAPIKey != "" { req.Header.Add("Authorization", henrikAPIKey) }

	resp, err := httpClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "라이엇 정보를 가져올 수 없습니다."})
		return
	}
	defer resp.Body.Close()

	var apiRes map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&apiRes)
	userData, ok := apiRes["data"].(map[string]interface{})
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "플레이어를 찾을 수 없습니다."})
		return
	}

	if mongoClient != nil {
		mongoClient.Database("valorant").Collection("playerAccounts").InsertOne(context.Background(), userData)
	}
	c.JSON(http.StatusOK, userData)
}

func getMatches(c *gin.Context) {
	puuid := c.Param("puuid")
	region := c.DefaultQuery("region", "kr")

	url := fmt.Sprintf("https://api.henrikdev.xyz/valorant/v3/by-puuid/matches/%s/%s?size=10", region, puuid)
	req, _ := http.NewRequest("GET", url, nil)
	if henrikAPIKey != "" { req.Header.Add("Authorization", henrikAPIKey) }

	resp, err := httpClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "경기 데이터를 가져올 수 없습니다."})
		return
	}
	defer resp.Body.Close()

	var res struct { Data []map[string]interface{} `json:"data"` }
	json.NewDecoder(resp.Body).Decode(&res)

	analyzedPlayers, findings := AnalyzeMatches(res.Data, puuid)

	c.JSON(http.StatusOK, gin.H{
		"matchesCount":    len(res.Data),
		"abusingDetected": len(findings) > 0,
		"details":         findings,
		"players":         analyzedPlayers,
	})
}

type PlayerStat struct {
	PUUID      string  `json:"puuid"`
	Name       string  `json:"name"`
	Met        int     `json:"met"`
	AsAlly     int     `json:"asAlly"`
	AsEnemy    int     `json:"asEnemy"`
	TargetLost int     `json:"targetLost"`
	Kills      float64 `json:"kills"`
	Deaths     float64 `json:"deaths"`
	Assists    float64 `json:"assists"`
	Score      float64 `json:"score"`
}

func AnalyzeMatches(matches []map[string]interface{}, targetPUUID string) ([]PlayerStat, []string) {
	statsMap := make(map[string]*PlayerStat)
	var findings []string

	for _, m := range matches {
		playersData, ok := m["players"].(map[string]interface{})
		if !ok { continue }
		allPlayers, ok := playersData["all_players"].([]interface{})
		if !ok { continue }

		var targetTeam string
		for _, p := range allPlayers {
			player, _ := p.(map[string]interface{})
			if pPUUID, _ := player["puuid"].(string); strings.EqualFold(pPUUID, targetPUUID) {
				targetTeam, _ = player["team"].(string)
				break
			}
		}

		teams, _ := m["teams"].(map[string]interface{})
		targetWon := false
		// 데스매치 등 팀이 없는 경우를 대비한 유연한 처리
		if teams != nil {
			teamKey := "red"
			if strings.EqualFold(targetTeam, "Blue") { teamKey = "blue" }
			if teamInfo, ok := teams[teamKey].(map[string]interface{}); ok {
				targetWon, _ = teamInfo["has_won"].(bool)
			}
		}

		for _, p := range allPlayers {
			player, ok := p.(map[string]interface{})
			if !ok { continue }
			pPUUID, _ := player["puuid"].(string)
			if strings.EqualFold(pPUUID, targetPUUID) { continue }

			pName, _ := player["name"].(string)
			pTag, _ := player["tag"].(string)
			pTeam, _ := player["team"].(string)
			
			var pKills, pDeaths, pAssists, pScore float64
			if pStats, ok := player["stats"].(map[string]interface{}); ok {
				pKills, _ = pStats["kills"].(float64)
				pDeaths, _ = pStats["deaths"].(float64)
				pAssists, _ = pStats["assists"].(float64)
				pScore, _ = pStats["score"].(float64)
			}

			if _, exists := statsMap[pPUUID]; !exists {
				statsMap[pPUUID] = &PlayerStat{PUUID: pPUUID, Name: pName + "#" + pTag}
			}

			s := statsMap[pPUUID]
			s.Met++
			s.Kills += pKills
			s.Deaths += pDeaths
			s.Assists += pAssists
			s.Score += pScore

			// 팀이 있고 같으면 아군, 아니면 적군 (데스매치는 모두 적군으로 판정)
			if pTeam != "" && strings.EqualFold(pTeam, targetTeam) { 
				s.AsAlly++ 
			} else { 
				s.AsEnemy++ 
			}
			if !targetWon { s.TargetLost++ }
		}
	}

	// 결과가 없을 경우 null 대신 빈 배열([]) 반환을 보장
	results := make([]PlayerStat, 0)
	for _, s := range statsMap {
		results = append(results, *s)
		if s.AsEnemy >= 3 {
			lossRatio := float64(s.TargetLost) / float64(s.Met)
			if lossRatio >= 0.75 {
				findings = append(findings, fmt.Sprintf("적군 [%s]와 %d번 만나 %d번 패배 (어뷰징 의심)", s.Name, s.Met, s.TargetLost))
			}
		}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Met > results[j].Met
	})

	return results, findings
}