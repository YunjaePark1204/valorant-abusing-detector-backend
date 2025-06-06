package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	// ginCors "github.com/gin-contrib/cors" // <- 이 라인 삭제!

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-lambda-go/events"
	ginadapter "github.com/awslabs/aws-lambda-go-api-proxy/gin"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// 전역 변수로 MongoDB 클라이언트와 Riot API 클라이언트 선언
var (
	mongoClient   *mongo.Client
	riotAPIKey    string
	riotAPIClient *http.Client
	ginLambda     *ginadapter.GinLambda // Gin 어댑터 인스턴스
)

// init 함수: 서버리스 함수가 콜드 스타트될 때 한 번만 실행됩니다.
// 여기서 MongoDB 연결 및 Riot API 키 설정을 초기화합니다.
func init() {
	log.Println("Netlify Function 초기화 시작...")

	// MongoDB 연결
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

	err = mongoClient.Ping(context.Background(), nil)
	if err != nil {
		log.Fatalf("MongoDB에 연결되었지만 핑 실패: %v", err)
	}
	log.Println("MongoDB에 성공적으로 연결되었습니다!")

	// Riot API 키 설정
	riotAPIKey = os.Getenv("RIOT_API_KEY")
	if riotAPIKey == "" {
		log.Fatal("환경 변수 RIOT_API_KEY가 설정되지 않았습니다.")
	}
	// httpClient 초기화는 init()에서 한 번만 합니다.
	riotAPIClient = &http.Client{Timeout: 10 * time.Second}

	// Gin 라우터 설정
	r := gin.Default() // 기본 로거와 복구 미들웨어 포함

	// ----------------------------------------------------
	// CORS 미들웨어 제거! 이제 _headers 파일로 처리합니다.
	// ----------------------------------------------------
	// corsConfig := ginCors.DefaultConfig()
	// corsConfig.AllowOrigins = []string{
	// 	"http://localhost:5173",
	// 	"https://valorant-abusing-frontend.vercel.app",
	// }
	// corsConfig.AllowMethods = []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"}
	// corsConfig.AllowHeaders = []string{"Origin", "Content-Type", "Accept", "Authorization"}
	// corsConfig.AllowCredentials = true
	// corsConfig.MaxAge = 300
	// r.Use(ginCors.New(corsConfig))
	// ----------------------------------------------------

	// 기존 Gin 라우터들을 등록
	r.GET("/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": "pong"})
	})

	r.GET("/account/riotid", func(c *gin.Context) {
		gameName := c.Query("gameName")
		tagLine := c.Query("tagLine")

		if gameName == "" || tagLine == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "gameName과 tagLine을 모두 제공해야 합니다."})
			return
		}

		// MongoDB에서 데이터 확인
		collection := mongoClient.Database("valorant").Collection("playerAccounts")
		var result map[string]interface{}
		filter := bson.M{"gameName": gameName, "tagLine": tagLine}
		err := collection.FindOne(context.Background(), filter).Decode(&result)

		if err == nil {
			log.Println("플레이어 계정 정보 MongoDB에서 로드됨:", result["puuid"])
			c.JSON(http.StatusOK, result)
			return
		}
		if err != mongo.ErrNoDocuments {
			log.Printf("MongoDB 조회 오류: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "데이터베이스 조회 오류"})
			return
		}

		// MongoDB에 없으면 Riot API 호출
		url := "https://asia.api.riotgames.com/riot/account/v1/accounts/by-riot-id/" + gameName + "/" + tagLine
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			log.Printf("Riot API 요청 생성 오류: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Riot API 요청 생성 오류"})
			return
		}
		req.Header.Add("X-Riot-Token", riotAPIKey) // 전역 riotAPIKey 사용

		resp, err := riotAPIClient.Do(req) // 전역 riotAPIClient 사용
		if err != nil {
			log.Printf("Riot API 호출 실패: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Riot API 호출 오류"})
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			log.Printf("Riot API (계정 정보) 오류 (HTTP %d): %s", resp.StatusCode, resp.Status)
			if resp.StatusCode == http.StatusNotFound {
				c.JSON(http.StatusNotFound, gin.H{"error": "해당 Riot ID를 찾을 수 없습니다."})
			} else {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Riot API 호출 오류"})
			}
			return
		}

		var apiResponse map[string]interface{}
		if err := json.NewDecoder(resp.Body).Decode(&apiResponse); err != nil {
			log.Printf("Riot API 응답 디코딩 오류: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Riot API 응답 디코딩 오류"})
			return
		}

		// MongoDB에 저장
		_, err = collection.InsertOne(context.Background(), apiResponse)
		if err != nil {
			log.Printf("MongoDB 저장 오류: %v", err)
		} else {
			log.Println("플레이어 계정 정보 MongoDB에 저장됨:", apiResponse["puuid"])
		}

		c.JSON(http.StatusOK, apiResponse)
	})

	// TODO: '/player/matches/:puuid' 라우터도 여기에 추가해야 합니다.
	// r.GET("/player/matches/:puuid", func(c *gin.Context) { ... })

	// Gin 라우터를 Lambda 어댑터에 연결
	ginLambda = ginadapter.New(r)
	log.Println("서버리스 함수 초기화 완료.")
}

// Handler 함수: Netlify(Lambda) 런타임에서 호출될 실제 핸들러 함수
func Handler(ctx context.Context, req events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
	return ginLambda.ProxyWithContext(ctx, req)
}

// main 함수: Netlify(Lambda) 런타임에 Handler를 등록합니다.
func main() {
	lambda.Start(Handler)
}
