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
	// 타임아웃 15초 설정 (Vercel 제한 내에서 최대한 기다림)
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

	// 🔥 동시 요청 차단(DDOS 방어)을 우회하기 위해 '순차적'으로 조회합니다.
	// 사용자가 언급한 데스매치(deathmatch)와 경쟁전(competitive)을 명시적으로 포함
	filters := []string{"", "competitive", "deathmatch", "unrated"}
	var allMatches []map[string]interface{}

	for _, f := range filters {
		// size=15로 여유있게 요청
		url := fmt.Sprintf("https://api.henrikdev.xyz/valorant/v3/by-puuid/matches/%s/%s?size=15", region, puuid)
		if f != "" {
			url += "&filter=" + f
		}

		req, _ := http.NewRequest("GET", url, nil)
		if henrikAPIKey != "" { req.Header.Add("Authorization", henrikAPIKey) }

		resp, err := httpClient.Do(req)
		if err != nil {
			continue // 에러나면 다음 필터로 조용히 넘어감
		}
		
		if resp.StatusCode == 200 {
			var res struct { Data []map[string]interface{} `json:"data"` }
			if err := json.NewDecoder(resp.Body).Decode(&res); err == nil && len(res.Data) > 0 {
				allMatches = append(allMatches, res.Data...)
			}
		}
		resp.Body.Close()
	}

	// 중복된 매치 제거 (전체조회 10개와 경쟁전조회 10개가 겹칠 수 있으므로)
	uniqueMatchesMap := make(map[string]map[string]interface{})
	for _, m := range allMatches {
		if meta, ok := m["metadata"].(map[string]interface{}); ok {
			if matchId, ok := meta["matchid"].(string); ok {
				uniqueMatchesMap[matchId] = m
			}
		}
	}

	var uniqueMatches []map[string]interface{}
	for _, m := range uniqueMatchesMap {
		uniqueMatches = append(uniqueMatches, m)
	}

	// 최신순 정렬 (game_start 기준 내림차순)
	sort.Slice(uniqueMatches, func(i, j int) bool {
		metaI, _ := uniqueMatches[i]["metadata"].(map[string]interface{})
		metaJ, _ := uniqueMatches[j]["metadata"].(map[string]interface{})
		startI, _ := metaI["game_start"].(float64)
		startJ, _ := metaJ["game_start"].(float64)
		return startI > startJ
	})

	// 데이터가 아예 없는 경우 방어 코드
	if len(uniqueMatches) == 0 {
		c.JSON(http.StatusOK, gin.H{
			"matchesCount":    0,
			"abusingDetected": false,
			"details":         []string{},
			"players":         []PlayerStat{},
			"history":         []MatchSummary{},
		})
		return
	}

	analyzedPlayers, findings, histories := AnalyzeMatches(uniqueMatches, puuid)

	c.JSON(http.StatusOK, gin.H{
		"matchesCount":    len(uniqueMatches),
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

		// 데스매치 등은 teams 데이터가 없으므로 무승부(-)로 처리됨
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