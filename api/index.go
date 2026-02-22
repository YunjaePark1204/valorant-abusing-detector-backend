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
	// 1. MongoDB 초기화
	mongoURI := os.Getenv("MONGO_URI")
	if mongoURI != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		clientOptions := options.Client().ApplyURI(mongoURI)
		client, err := mongo.Connect(ctx, clientOptions)
		if err != nil {
			log.Printf("MongoDB 연결 실패: %v", err)
		} else {
			// 연결 확인
			if err := client.Ping(ctx, nil); err != nil {
				log.Printf("MongoDB Ping 실패: %v", err)
			} else {
				log.Println("MongoDB 연결 성공")
				mongoClient = client
			}
		}
	} else {
		log.Println("경고: MONGO_URI 환경변수가 설정되지 않았습니다.")
	}

	// 2. Henrik API 설정
	henrikAPIKey = os.Getenv("HENRIK_API_KEY")
	if henrikAPIKey == "" {
		log.Println("경고: HENRIK_API_KEY 환경변수가 설정되지 않았습니다.")
	}
	httpClient = &http.Client{Timeout: 15 * time.Second}

	// 3. Gin 라우터 설정
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	// 라우트 등록 (경로 앞에 /api 유지)
	r.GET("/api/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": "pong"})
	})
	// DB 상태 확인용 엔드포인트
	r.GET("/api/dbstatus", dbStatus)
	r.GET("/api/account/riotid", getAccount)
	r.GET("/api/player/matches/:puuid", getMatches)

	router = r
}

// Vercel 진입점 핸들러 (CORS 직접 처리)
func Handler(w http.ResponseWriter, r *http.Request) {
	// 허용할 origin 리스트 (프로덕션 및 개발용)
	allowedOrigins := map[string]bool{
		"https://valorant-abusing-frontend.vercel.app": true,
		"http://localhost:5173": true,
		"http://localhost:3000": true,
	}

	// 요청의 origin 확인 후 CORS 헤더 설정
	origin := r.Header.Get("Origin")
	if allowedOrigins[origin] {
		w.Header().Set("Access-Control-Allow-Origin", origin)
	}
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS, PUT, DELETE")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	w.Header().Set("Access-Control-Allow-Credentials", "true")

	// Preflight 요청 처리
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	router.ServeHTTP(w, r)
}

func getAccount(c *gin.Context) {
	name := c.Query("gameName")
	tag := c.Query("tagLine")

	log.Printf("[getAccount] 요청 받음 - gameName: %s, tagLine: %s", name, tag)

	if name == "" || tag == "" {
		log.Printf("[getAccount] 에러: 파라미터 누락")
		c.JSON(http.StatusBadRequest, gin.H{"error": "gameName과 tagLine이 필요합니다."})
		return
	}

	if mongoClient == nil {
		log.Printf("[getAccount] 에러: mongoClient가 nil입니다")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "DB 연결 실패"})
		return
	}
	
	log.Printf("[getAccount] mongoClient 정상, 데이터베이스 접근 시도")

	col := mongoClient.Database("valorant_abusing_detector").Collection("players")
	var result map[string]interface{}
	if err := col.FindOne(context.Background(), bson.M{"name": name, "tag": tag}).Decode(&result); err == nil {
		log.Printf("[getAccount] 캐시에서 데이터 찾음: %s/%s", name, tag)
		c.JSON(http.StatusOK, result)
		return
	}
	
	log.Printf("[getAccount] 캐시에 없음, Henrik API 호출")

	url := fmt.Sprintf("https://api.henrikdev.xyz/valorant/v1/account/%s/%s", name, tag)
	req, _ := http.NewRequest("GET", url, nil)
	if henrikAPIKey != "" { req.Header.Add("Authorization", henrikAPIKey) }

	resp, err := httpClient.Do(req)
	if err != nil {
		log.Printf("Henrik API 요청 실패: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "라이엇 계정 정보 조회 실패"})
		return
	}
	if resp.StatusCode != 200 {
		log.Printf("Henrik API 오류 상태: %d", resp.StatusCode)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "라이엇 계정 정보 조회 실패"})
		return
	}
	defer resp.Body.Close()

	var apiRes map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&apiRes); err != nil {
		log.Printf("JSON 디코딩 실패: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "응답 처리 실패"})
		return
	}
	
	userData, ok := apiRes["data"].(map[string]interface{})
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "플레이어를 찾을 수 없습니다."})
		return
	}

	col.InsertOne(context.Background(), userData)
	// 응답에 puuid 포함하여 프론트엔드에서 쉽게 접근 가능하게 함
	c.JSON(http.StatusOK, gin.H{
		"puuid": userData["puuid"],
		"name": userData["name"],
		"tag": userData["tag"],
		"data": userData,
	})
}

func getMatches(c *gin.Context) {
	puuid := c.Param("puuid")
	region := c.DefaultQuery("region", "kr")

	if puuid == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "puuid가 필요합니다."})
		return
	}

	url := fmt.Sprintf("https://api.henrikdev.xyz/valorant/v3/by-puuid/matches/%s/%s", region, puuid)
	req, _ := http.NewRequest("GET", url, nil)
	if henrikAPIKey != "" { req.Header.Add("Authorization", henrikAPIKey) }

	resp, err := httpClient.Do(req)
	if err != nil {
		log.Printf("Henrik API 매치 조회 실패: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "경기 데이터 로드 실패"})
		return
	}
	if resp.StatusCode != 200 {
		log.Printf("Henrik API 매치 오류 상태: %d", resp.StatusCode)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "경기 데이터 로드 실패"})
		return
	}
	defer resp.Body.Close()

	var res struct { Data []map[string]interface{} `json:"data"` }
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		log.Printf("JSON 디코딩 실패: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "응답 처리 실패"})
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
			player := p.(map[string]interface{})
			if player["puuid"] == targetPUUID {
				targetTeam = player["team"].(string)
				break
			}
		}

		teams, _ := m["teams"].(map[string]interface{})
		teamKey := "red"; if targetTeam == "Blue" { teamKey = "blue" }
		teamInfo, _ := teams[teamKey].(map[string]interface{})
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

// dbStatus returns current MongoDB connection status and players collection count
func dbStatus(c *gin.Context) {
	if mongoClient == nil {
		log.Printf("[dbStatus] mongoClient is nil")
		c.JSON(http.StatusInternalServerError, gin.H{"connected": false, "error": "mongoClient is nil"})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := mongoClient.Ping(ctx, nil); err != nil {
		log.Printf("[dbStatus] MongoDB ping failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"connected": false, "error": err.Error()})
		return
	}

	col := mongoClient.Database("valorant_abusing_detector").Collection("players")
	cnt, err := col.CountDocuments(ctx, bson.M{})
	if err != nil {
		log.Printf("[dbStatus] CountDocuments failed: %v", err)
		c.JSON(http.StatusOK, gin.H{"connected": true, "countError": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"connected": true, "playersCount": cnt})
}