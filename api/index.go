package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	ginCors "github.com/gin-contrib/cors"

	"github.com/aws/aws-lambda-go/lambda" // lambda 임포트는 유지 (Handler 시그니처에 사용)
	"github.com/aws/aws-lambda-go/events" // events 임포트는 유지 (Handler 시그니처에 사용)
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
	log.Println("서버리스 함수 초기화 시작...")

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
	riotAPIClient = &http.Client{Timeout: 10 * time.Second} // Riot API 클라이언트 초기화

	// Gin 라우터 설정
	r := gin.Default() // 기본 로거와 복구 미들웨어 포함
// CORS 미들웨어 설정
	corsConfig := ginCors.DefaultConfig()
    corsConfig.AllowOrigins = []string{
        "http://localhost:5173/", // 로컬 개발 서버 URL

        // =========== test
        // 슬래시 제거
        "https://valorant-abusing-frontend.vercel.app/", // 여기에 당신의 Vercel 프론트엔드 URL을 정확히 입력하세요!
    }
    corsConfig.AllowMethods = []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"}
    corsConfig.AllowHeaders = []string{"Origin", "Content-Type", "Accept", "Authorization"}
    corsConfig.AllowCredentials = true
    corsConfig.MaxAge = 300
    r.Use(ginCors.New(corsConfig))

    // CORS 용 OPTIONS 라우팅 추가 
    r.OPTIONS("/path", func(cgin.Context) {
    c.Status(http.StatusOK)
})

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
		req.Header.Add("X-Riot-Token", riotAPIKey)

		resp, err := riotAPIClient.Do(req)
		if err != nil {
			log.Printf("Riot API 호출 실패: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Riot API 호출 오류"})
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			log.Printf("Riot API (계정 정보) 오류 (HTTP %d): %s", resp.StatusCode, resp.Status)
			// 라이엇 API가 404 (찾을 수 없음) 응답을 보낼 경우
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
			// 저장 오류라도 사용자에게는 성공 응답을 보낼 수 있음
		} else {
			log.Println("플레이어 계정 정보 MongoDB에 저장됨:", apiResponse["puuid"])
		}

		c.JSON(http.StatusOK, apiResponse)
	})

	// TODO: '/player/matches/:puuid' 라우터도 여기에 추가해야 합니다.
	// 기존 main.go의 해당 라우터 코드를 이리로 옮기세요.
	// r.GET("/player/matches/:puuid", func(c *gin.Context) { ... })

	// Gin 라우터를 Lambda 어댑터에 연결
	ginLambda = ginadapter.New(r)
	log.Println("서버리스 함수 초기화 완료.")
}

// Vercel(Lambda)에서 호출될 실제 핸들러 함수
// 이 함수가 모든 HTTP 요청을 Gin 라우터로 전달합니다.
// main 함수가 없으므로 이 Handler 함수가 Vercel 런타임에 의해 직접 호출됩니다.
func Handler(ctx context.Context, req events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
	// GinLambda 핸들러를 사용하여 요청 처리
	// ginLambda는 init() 함수에서 이미 초기화되었습니다.
	return ginLambda.ProxyWithContext(ctx, req)
}

// main 함수는 완전히 제거됩니다.
/*
func main() {
    lambda.Start(func(ctx context.Context, req events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
        return ginLambda.ProxyWithContext(ctx, req)
    })
}
*/
