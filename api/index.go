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
	"go.mongodb.org/mongo-driver/bson/primitive"
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
	httpClient = &http.Client{Timeout: 8 * time.Second} // Vercel 10초 컷을 피하기 위해 8초 설정

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
		// DB 타임아웃 3초 추가
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := col.FindOne(ctx, bson.M{"name": name, "tag": tag}).Decode(&result); err == nil {
			c.JSON(http.StatusOK, result)
			return
		}
	}

	url := fmt.Sprintf("https://api.henrikdev.xyz/valorant/v1/account/%s/%s", name, tag)
	req, _ := http.NewRequest("GET", url, nil)
	if henrikAPIKey != "" { req.Header.Add("Authorization", henrikAPIKey) }

	resp, err := httpClient.Do(req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "라이엇 서버와 통신할 수 없습니다."})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		c.JSON(http.StatusNotFound, gin.H{"error": "플레이어를 찾을 수 없거나 API 제한에 걸렸습니다."})
		return
	}

	var apiRes map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&apiRes)
	userData, ok := apiRes["data"].(map[string]interface{})
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "플레이어를 찾을 수 없습니다."})
		return
	}

	if mongoClient != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		mongoClient.Database("valorant").Collection("playerAccounts").InsertOne(ctx, userData)
	}
	c.JSON(http.StatusOK, userData)
}

func getMatches(c *gin.Context) {
	puuid := c.Param("puuid")
	region := c.DefaultQuery("region", "kr")

	url := fmt.Sprintf("https://api.henrikdev.xyz/valorant/v3/by-puuid/matches/%s/%s?size=15", region, puuid)
	req, _ := http.NewRequest("GET", url, nil)
	if henrikAPIKey != "" { req.Header.Add("Authorization", henrikAPIKey) }

	var newMatches []map[string]interface{}
	resp, err := httpClient.Do(req)
	if err == nil {
		defer resp.Body.Close() // 메모리 누수 방지
		if resp.StatusCode == 200 {
			var res struct { Data []map[string]interface{} `json:"data"` }
			if err := json.NewDecoder(resp.Body).Decode(&res); err == nil && len(res.Data) > 0 {
				newMatches = res.Data
			}
		}
	}

	var finalMatches []map[string]interface{}

	if mongoClient != nil {
		col := mongoClient.Database("valorant").Collection("matches")

		// 🔥 서버 터짐 방지: DB 작업이 4초 이상 걸리면 즉시 포기하고 빠져나오는 안전장치
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()

		for _, m := range newMatches {
			if meta, ok := m["metadata"].(map[string]interface{}); ok {
				if matchId, ok := meta["matchid"].(string); ok {
					opts := options.Update().SetUpsert(true)
					filter := bson.M{"metadata.matchid": matchId}
					update := bson.M{"$set": m}
					// 에러가 나더라도 무시하고 진행
					col.UpdateOne(ctx, filter, update, opts)
				}
			}
		}

		filter := bson.M{"players.all_players.puuid": primitive.Regex{Pattern: "^" + puuid + "$", Options: "i"}}
		findOpts := options.Find().SetSort(bson.D{{"metadata.game_start", -1}}).SetLimit(100)
		
		cursor, err := col.Find(ctx, filter, findOpts)
		if err == nil {
			var dbMatches []map[string]interface{}
			if err = cursor.All(ctx, &dbMatches); err == nil && len(dbMatches) > 0 {
				finalMatches = dbMatches // 성공하면 DB에 쌓인 전적 사용
			} else {
				finalMatches = newMatches // 실패하면 방금 가져온 전적만 사용
			}
		} else {
			finalMatches = newMatches
		}
	} else {
		finalMatches = newMatches
	}

	if len(finalMatches) == 0 {
		c.JSON(http.StatusOK, gin.H{
			"matchesCount":    0,
			"abusingDetected": false,
			"details":         []string{},
			"players":         []PlayerStat{},
			"history":         []MatchSummary{},
		})
		return
	}

	analyzedPlayers, findings, histories := AnalyzeMatches(finalMatches, puuid)

	c.JSON(http.StatusOK, gin.H{
		"matchesCount":    len(finalMatches),
		"abusingDetected": len(findings) > 0,
		"details":         findings,
		"players":         analyzedPlayers,
		"history":         histories,
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

type MatchSummary struct {
	MatchID string  `json:"matchId"`
	Map     string  `json:"map"`
	Mode    string  `json:"mode"`
	Agent   string  `json:"agent"`
	Result  string  `json:"result"`
	Kills   float64 `json:"kills"`
	Deaths  float64 `json:"deaths"`
	Assists float64 `json:"assists"`
	Score   float64 `json:"score"`
}

func AnalyzeMatches(matches []map[string]interface{}, targetPUUID string) ([]PlayerStat, []string, []MatchSummary) {
	statsMap := make(map[string]*PlayerStat)
	var findings []string
	var histories []MatchSummary

	for _, m := range matches {
		meta, ok := m["metadata"].(map[string]interface{})
		if !ok { continue }
		matchId, _ := meta["matchid"].(string)
		mapName, _ := meta["map"].(string)
		mode, _ := meta["mode"].(string)

		playersData, ok := m["players"].(map[string]interface{})
		if !ok { continue }
		allPlayers, ok := playersData["all_players"].([]interface{})
		if !ok { continue }

		var targetTeam, targetAgent string
		var targetKills, targetDeaths, targetAssists, targetScore float64

		for _, p := range allPlayers {
			player, _ := p.(map[string]interface{})
			if pPUUID, _ := player["puuid"].(string); strings.EqualFold(pPUUID, targetPUUID) {
				targetTeam, _ = player["team"].(string)
				targetAgent, _ = player["character"].(string)
				if stats, ok := player["stats"].(map[string]interface{}); ok {
					targetKills, _ = stats["kills"].(float64)
					targetDeaths, _ = stats["deaths"].(float64)
					targetAssists, _ = stats["assists"].(float64)
					targetScore, _ = stats["score"].(float64)
				}
				break
			}
		}

		teams, _ := m["teams"].(map[string]interface{})
		targetWon := false
		resultStr := "-"

		if teams != nil && targetTeam != "" {
			teamKey := "red"
			if strings.EqualFold(targetTeam, "Blue") { teamKey = "blue" }
			if teamInfo, ok := teams[teamKey].(map[string]interface{}); ok {
				targetWon, _ = teamInfo["has_won"].(bool)
				roundsWon, _ := teamInfo["rounds_won"].(float64)
				roundsLost, _ := teamInfo["rounds_lost"].(float64)
				
				if targetWon {
					resultStr = "승리"
				} else if roundsWon == roundsLost && roundsWon > 0 {
					resultStr = "무승부"
				} else {
					resultStr = "패배"
				}
			}
		}

		histories = append(histories, MatchSummary{
			MatchID: matchId,
			Map:     mapName,
			Mode:    mode,
			Agent:   targetAgent,
			Result:  resultStr,
			Kills:   targetKills,
			Deaths:  targetDeaths,
			Assists: targetAssists,
			Score:   targetScore,
		})

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

			if pTeam != "" && strings.EqualFold(pTeam, targetTeam) { 
				s.AsAlly++ 
			} else { 
				s.AsEnemy++ 
			}
			if !targetWon { s.TargetLost++ }
		}
	}

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

	sort.Slice(results, func(i, j int) bool { return results[i].Met > results[j].Met })

	return results, findings, histories
}