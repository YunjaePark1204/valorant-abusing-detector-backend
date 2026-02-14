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
	ginadapter "github.com/awslabs/aws-lambda-go-api-proxy/gin"
	"github.com/aws/aws-lambda-go/events"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

var (
	mongoClient   *mongo.Client
	henrikAPIKey  string
	httpClient    *http.Client
	ginLambda     *ginadapter.GinLambda
)

func init() {
	log.Println("서버리스 함수 초기화 시작...")

	// MongoDB 연결 설정
	mongoURI := os.Getenv("MONGO_URI")
	if mongoURI == "" {
		log.Fatal("환경 변수 MONGO_URI가 설정되지 않았습니다.")
	}
	clientOptions := options.Client().ApplyURI(mongoURI)
	var err error
	mongoClient, err = mongo.Connect(context.Background(), clientOptions)
	if err != nil {
		log.Fatalf("MongoDB 연결 실패: %v", err)
	}

	// HenrikDev API 설정 (API 키는 헤더 Authorization에 사용될 수 있음)
	henrikAPIKey = os.Getenv("HENRIK_API_KEY") 
	httpClient = &http.Client{Timeout: 15 * time.Second}

	r := gin.Default()

	// 1. Health Check
	r.GET("/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": "pong"})
	})

	// 2. 계정 정보 조회 (HenrikDev v1/account 사용)
	r.GET("/account/riotid", func(c *gin.Context) {
		gameName := c.Query("gameName")
		tagLine := c.Query("tagLine")

		if gameName == "" || tagLine == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "gameName과 tagLine이 필요합니다."})
			return
		}

		collection := mongoClient.Database("valorant").Collection("playerAccounts")
		var result map[string]interface{}
		filter := bson.M{"name": gameName, "tag": tagLine}
		err := collection.FindOne(context.Background(), filter).Decode(&result)

		if err == nil {
			c.JSON(http.StatusOK, result)
			return
		}

		// HenrikDev API 호출
		url := fmt.Sprintf("https://api.henrikdev.xyz/valorant/v1/account/%s/%s", gameName, tagLine)
		req, _ := http.NewRequest("GET", url, nil)
		if henrikAPIKey != "" {
			req.Header.Add("Authorization", henrikAPIKey)
		}

		resp, err := httpClient.Do(req)
		if err != nil || resp.StatusCode != http.StatusOK {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "API 호출 실패"})
			return
		}
		defer resp.Body.Close()

		var apiResponse map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&apiResponse)
		userData := apiResponse["data"].(map[string]interface{})

		collection.InsertOne(context.Background(), userData)
		c.JSON(http.StatusOK, userData)
	})

	// 3. 경기 데이터 수집 및 어뷰징 탐지 (HenrikDev v3/matches 사용)
	r.GET("/player/matches/:puuid", func(c *gin.Context) {
		puuid := c.Param("puuid")
		region := c.DefaultQuery("region", "kr") // 기본 지역 한국

		// HenrikDev API: 최근 경기 상세 정보를 한 번에 가져옴
		url := fmt.Sprintf("https://api.henrikdev.xyz/valorant/v3/by-puuid/matches/%s/%s", region, puuid)
		req, _ := http.NewRequest("GET", url, nil)
		if henrikAPIKey != "" {
			req.Header.Add("Authorization", henrikAPIKey)
		}

		resp, err := httpClient.Do(req)
		if err != nil || resp.StatusCode != http.StatusOK {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "경기 데이터 로드 실패"})
			return
		}
		defer resp.Body.Close()

		var apiResponse struct {
			Data []map[string]interface{} `json:"data"`
		}
		json.NewDecoder(resp.Body).Decode(&apiResponse)

		matchesCollection := mongoClient.Database("valorant").Collection("matches")
		for _, match := range apiResponse.Data {
			// 중복 저장 방지 및 저장
			meta := match["metadata"].(map[string]interface{})
			matchesCollection.UpdateOne(
				context.Background(),
				bson.M{"metadata.matchid": meta["matchid"]},
				bson.M{"$set": match},
				options.Update().SetUpsert(true),
			)
		}

		// 어뷰징 탐지 로직 실행
		findings := CheckForAbusing(apiResponse.Data, puuid)

		c.JSON(http.StatusOK, gin.H{
			"puuid": puuid,
			"processedMatches": len(apiResponse.Data),
			"abusingDetected": len(findings) > 0,
			"abusingDetails": findings,
		})
	})

	ginLambda = ginadapter.New(r)
}

// OpponentInteraction: 상대방과의 전적 통계
type OpponentInteraction struct {
	PUUID         string
	MatchesMet    int
	WinsAgainst   int
	LossesAgainst int
	TotalKDA      float64
}

// HenrikDev 데이터 구조에 맞게 수정된 어뷰징 탐지 로직
func CheckForAbusing(matches []map[string]interface{}, targetPUUID string) []string {
	var findings []string
	opponentStats := make(map[string]*OpponentInteraction)

	for _, match := range matches {
		players, _ := match["players"].(map[string]interface{})
		allPlayers, _ := players["all_players"].([]interface{})
		
		var targetTeam string
		var targetKDA float64
		
		// 대상 플레이어 정보 추출
		for _, p := range allPlayers {
			player := p.(map[string]interface{})
			if player["puuid"] == targetPUUID {
				targetTeam = player["team"].(string)
				stats := player["stats"].(map[string]interface{})
				k := stats["kills"].(float64)
				d := math.Max(1.0, stats["deaths"].(float64))
				a := stats["assists"].(float64)
				targetKDA = (k + a) / d
				break
			}
		}

		// 승리 여부 확인 (HenrikDev v3 구조)
		teams := match["teams"].(map[string]interface{})
		targetTeamColor := "red"
		if targetTeam == "Blue" { targetTeamColor = "blue" }
		teamInfo := teams[targetTeamColor].(map[string]interface{})
		won := teamInfo["has_won"].(bool)

		// 상대방과의 상호작용 집계
		for _, p := range allPlayers {
			opp := p.(map[string]interface{})
			oppPUUID := opp["puuid"].(string)
			if oppPUUID == targetPUUID || opp["team"] == targetTeam {
				continue
			}

			if _, exists := opponentStats[oppPUUID]; !exists {
				opponentStats[oppPUUID] = &OpponentInteraction{PUUID: oppPUUID}
			}
			stats := opponentStats[oppPUUID]
			stats.MatchesMet++
			stats.TotalKDA += targetKDA
			if won { stats.WinsAgainst++ } else { stats.LossesAgainst++ }
		}
	}

	// 임계값 분석 (예: 특정 상대를 5번 이상 만나고 패배율이 80% 이상인 경우)
	for _, s := range opponentStats {
		if s.MatchesMet >= 5 {
			lossRatio := float64(s.LossesAgainst) / float64(s.MatchesMet)
			if lossRatio >= 0.8 {
				findings = append(findings, fmt.Sprintf("상대 PUUID: %s와 %d번 대전, 패배율 %.2f (고의 패배 의심)", s.PUUID, s.MatchesMet, lossRatio))
			}
		}
	}

	return findings
}

func Handler(ctx context.Context, req events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
	resp, err := ginLambda.ProxyWithContext(ctx, req)
	if resp.Headers == nil {
		resp.Headers = map[string]string{}
	}
	// CORS 설정
	resp.Headers["Access-Control-Allow-Origin"] = "*" 
	resp.Headers["Access-Control-Allow-Methods"] = "GET, POST, OPTIONS"
	resp.Headers["Access-Control-Allow-Headers"] = "Content-Type, Authorization"
	return resp, err
}

func main() {
	// 로컬 실행용 (Vercel에서는 사용되지 않음)
	// lambda.Start(Handler)
}