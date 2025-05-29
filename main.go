package main

import (
	"context" // 컨텍스트를 사용하여 API 호출 및 DB 작업 타임아웃/취소 관리
	"encoding/json" // JSON 데이터를 파싱하기 위해 사용
	"fmt"
	"log" // 로깅을 위해 사용
	"math" // KDA 계산 시 0으로 나누는 것을 방지하기 위해 사용
	"net/http" // HTTP 상태 코드 사용
	"os" // 환경 변수를 읽기 위해 사용
	"strconv" // 문자열을 숫자로 변환하기 위해 사용
	"time" // 시간 관련 작업 (타임아웃, Rate Limiter)

	"github.com/gin-gonic/gin" // Gin 웹 프레임워크 임포트
	"go.mongodb.org/mongo-driver/bson" // MongoDB 쿼리 필터 생성을 위해 추가
	"go.mongodb.org/mongo-driver/mongo" // MongoDB 드라이버 임포트
	"go.mongodb.org/mongo-driver/mongo/options" // MongoDB 연결 옵션 설정
	"go.mongodb.org/mongo-driver/mongo/readpref" // MongoDB 연결 확인용

	"golang.org/x/time/rate" // Rate Limiter 라이브러리 임포트

	"github.com/rs/cors" // CORS 라이브러리 임포트
)

// OpponentInteraction은 특정 플레이어와 한 명의 상대방 간의 누적된 경기 통계를 저장합니다.
type OpponentInteraction struct {
	PUUID         string  // 상대방의 PUUID
	MatchesMet    int     // 상대방과 함께 플레이한 총 경기 수
	WinsAgainst   int     // 플레이어가 상대방을 만났을 때 이긴 횟수
	LossesAgainst int     // 플레이어가 상대방을 만났을 때 진 횟수
	TotalKDAAgainst float64 // 상대방과 만났을 때 플레이어의 KDA 총합 (평균 계산용)
}

// RiotAPIClient는 라이엇 API와 상호작용하는 구조체
type RiotAPIClient struct {
	apiKey      string
	httpClient  *http.Client
	rateLimiter *rate.Limiter // API 호출 제한을 위한 Rate Limiter
}

// NewRiotAPIClient는 RiotAPIClient 인스턴스를 생성합니다.
func NewRiotAPIClient(apiKey string) *RiotAPIClient {
	// 라이엇 개발자 키의 기본 Rate Limit (20 req / 1 sec, 100 req / 2 min)를 고려합니다.
	// 여기서는 1초에 최대 20번의 요청을 보낼 수 있도록 Rate Limiter를 설정합니다.
	// 실제 프로덕션 환경에서는 라이엇 API 응답 헤더의 'Rate-Limit' 정보를 파싱하여
	// 더 정교하게 제어하는 로직이 필요할 수 있습니다.
	limiter := rate.NewLimiter(rate.Every(time.Second/20), 20) // 1초에 20 요청, 버스트 20

	return &RiotAPIClient{
		apiKey:      apiKey,
		httpClient:  &http.Client{Timeout: 10 * time.Second}, // HTTP 요청 타임아웃 설정
		rateLimiter: limiter,
	}
}

// GetAccountByRiotID는 Riot ID(gameName, tagLine)로 계정 정보를 가져옵니다.
// 라이엇 API 문서: GET /riot/account/v1/accounts/by-riot-id/{gameName}/{tagLine}
func (c *RiotAPIClient) GetAccountByRiotID(gameName, tagLine string) (map[string]interface{}, error) {
	// Rate Limiter를 사용하여 API 호출 전에 대기합니다.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second) // 대기 타임아웃 설정
	defer cancel() // 함수 종료 시 컨텍스트 취소
	if err := c.rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("라이엇 API (계정 정보) 호출 제한 초과: %w", err)
	}

	// Riot API는 지역별 엔드포인트가 다르므로, 여기서는 "asia"를 사용합니다.
	// 필요에 따라 변경하거나 환경 설정으로 관리할 수 있습니다.
	url := fmt.Sprintf("https://asia.api.riotgames.com/riot/account/v1/accounts/by-riot-id/%s/%s?api_key=%s", gameName, tagLine, c.apiKey)

	resp, err := c.httpClient.Get(url) // HTTP GET 요청 전송
	if err != nil {
		return nil, fmt.Errorf("라이엇 API (계정 정보) 요청 실패: %w", err)
	}
	defer resp.Body.Close() // 응답 본문 닫기

	if resp.StatusCode != http.StatusOK { // 응답 상태 코드가 200 OK가 아닌 경우
		var apiError map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&apiError) // 에러 응답 본문 파싱

		if status, ok := apiError["status"].(map[string]interface{}); ok {
			if message, msgOk := status["message"].(string); msgOk {
				return nil, fmt.Errorf("라이엇 API (계정 정보) 오류 (%d): %s", resp.StatusCode, message)
			}
		}
		return nil, fmt.Errorf("라이엇 API (계정 정보) 오류 (상태 코드 %d)", resp.StatusCode)
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil { // 응답 본문 JSON 파싱
		return nil, fmt.Errorf("계정 정보 JSON 파싱 실패: %w", err)
	}

	return result, nil
}

// GetMatchListByPUUID는 PUUID로 플레이어의 경기 목록 (Match ID 배열)을 가져옵니다.
// 라이엇 API 문서: GET /val/match/v1/matches/by-puuid/{puuid}/history
func (c *RiotAPIClient) GetMatchListByPUUID(puuid string, count int) ([]string, error) {
	// Rate Limiter 대기
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second) // 대기 타임아웃
	defer cancel()
	if err := c.rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("라이엇 API (경기 목록) 호출 제한 초과: %w", err)
	}

	// count 파라미터는 가져올 경기 수를 제한합니다.
	url := fmt.Sprintf("https://asia.api.riotgames.com/val/match/v1/matches/by-puuid/%s/history?size=%d&api_key=%s", puuid, count, c.apiKey)

	resp, err := c.httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("라이엇 API (경기 목록) 요청 실패: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var apiError map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&apiError)
		if status, ok := apiError["status"].(map[string]interface{}); ok {
			if message, msgOk := status["message"].(string); msgOk {
				return nil, fmt.Errorf("라이엇 API (경기 목록) 오류 (%d): %s", resp.StatusCode, message)
			}
		}
		return nil, fmt.Errorf("라이엇 API (경기 목록) 오류 (상태 코드 %d)", resp.StatusCode)
	}

	var matchIDs []string
	if err := json.NewDecoder(resp.Body).Decode(&matchIDs); err != nil {
		return nil, fmt.Errorf("경기 ID 목록 JSON 파싱 실패: %w", err)
	}

	return matchIDs, nil
}

// GetMatchDetails는 Match ID로 경기 상세 정보를 가져옵니다.
// 라이엇 API 문서: GET /val/match/v1/matches/{matchId}
func (c *RiotAPIClient) GetMatchDetails(matchID string) (map[string]interface{}, error) {
	// Rate Limiter 대기
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second) // 대기 타임아웃
	defer cancel()
	if err := c.rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("라이엇 API (경기 상세) 호출 제한 초과: %w", err)
	}

	url := fmt.Sprintf("https://asia.api.riotgames.com/val/match/v1/matches/%s?api_key=%s", matchID, c.apiKey)

	resp, err := c.httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("라이엇 API (경기 상세) 요청 실패: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var apiError map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&apiError)
		if status, ok := apiError["status"].(map[string]interface{}); ok {
			if message, msgOk := status["message"].(string); msgOk {
				return nil, fmt.Errorf("라이엇 API (경기 상세) 오류 (%d): %s", resp.StatusCode, message)
			}
		}
		return nil, fmt.Errorf("라이엇 API (경기 상세) 오류 (상태 코드 %d)", resp.StatusCode)
	}

	var matchDetails map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&matchDetails); err != nil {
		return nil, fmt.Errorf("경기 상세 정보 JSON 파싱 실패: %w", err)
	}

	return matchDetails, nil
}

// CheckForAbusing은 MongoDB에 저장된 경기 데이터를 기반으로 어뷰징 패턴을 감지합니다.
// 특히 특정 상대방과의 상호작용 통계를 분석합니다.
// 반환 값: 발견된 어뷰징 패턴에 대한 상세 설명 문자열 배열, 에러
func CheckForAbusing(matchesColl *mongo.Collection, targetPUUID string) ([]string, error) {
	var suspiciousFindings []string

	// 1. MongoDB에서 대상 PUUID가 참여한 모든 경기 데이터를 가져옵니다.
	// 필터: "info.players.puuid" 필드에 targetPUUID가 포함된 문서
	filter := bson.M{"info.players.puuid": targetPUUID}
	cursor, err := matchesColl.Find(context.TODO(), filter)
	if err != nil {
		return nil, fmt.Errorf("MongoDB에서 '%s'의 경기 데이터 조회 실패: %w", targetPUUID, err)
	}
	defer cursor.Close(context.TODO()) // 함수 종료 시 커서 닫기

	var matches []map[string]interface{}
	if err = cursor.All(context.TODO(), &matches); err != nil {
		return nil, fmt.Errorf("경기 데이터 파싱 실패: %w", err)
	}

	if len(matches) < 5 { // 최소 경기 수 (예: 5회) 미만이면 분석 어려움
		return nil, nil // 또는 "충분한 경기 데이터가 없습니다."와 같은 메시지를 반환할 수 있습니다.
	}

	// 각 상대방 PUUID별 상호작용 통계를 저장할 맵
	opponentStats := make(map[string]*OpponentInteraction)

	// 2. 각 경기 데이터를 순회하며 상대방과의 상호작용 통계를 집계합니다.
	for _, match := range matches {
		info, ok := match["info"].(map[string]interface{})
		if !ok {
			// log.Printf("매치 ID %v: 'info' 필드 파싱 실패. 스킵합니다.", match["metadata"].(map[string]interface{})["matchId"])
			continue
		}

		playersRaw, ok := info["players"].([]interface{}) // "players" 필드는 배열
		if !ok {
			// log.Printf("매치 ID %v: 'players' 필드 파싱 실패. 스킵합니다.", match["metadata"].(map[string]interface{})["matchId"])
			continue
		}

		teamsRaw, ok := info["teams"].([]interface{}) // "teams" 필드는 배열
		if !ok {
			// log.Printf("매치 ID %v: 'teams' 필드 파싱 실패. 스킵합니다.", match["metadata"].(map[string]interface{})["matchId"])
			continue
		}

		// 현재 경기의 모든 플레이어를 map[PUUID]playerInfo 형태로 변환하여 쉽게 접근
		playersInMatch := make(map[string]map[string]interface{})
		for _, p := range playersRaw {
			player := p.(map[string]interface{})
			playersInMatch[player["puuid"].(string)] = player
		}

		// 현재 경기의 모든 팀을 map[teamID]teamInfo 형태로 변환
		teamsInMatch := make(map[string]map[string]interface{})
		for _, t := range teamsRaw {
			team := t.(map[string]interface{})
			teamsInMatch[team["teamId"].(string)] = team
		}

		// 대상 플레이어 정보 추출
		targetPlayerInfo, exists := playersInMatch[targetPUUID]
		if !exists {
			continue // 대상 플레이어가 이 경기에 없으면 스킵
		}

		targetStats, statsOk := targetPlayerInfo["stats"].(map[string]interface{})
		if !statsOk {
			// log.Printf("매치 ID %v: 대상 플레이어 통계 파싱 실패. 스킵합니다.", match["metadata"].(map[string]interface{})["matchId"])
			continue
		}

		// Riot API 응답에서 kills, deaths, assists는 float64로 올 수 있음
		targetKills := float64(targetStats["kills"].(float64))
		targetDeaths := float64(targetStats["deaths"].(float64))
		targetAssists := float64(targetStats["assists"].(float64))

		// KDA 계산 (데스가 0인 경우 1로 나누어 Infinity 방지)
		targetKDA := (targetKills + targetAssists) / math.Max(1.0, targetDeaths)
		targetTeamID := targetPlayerInfo["teamId"].(string) // 대상 플레이어의 팀 ID
		targetPlayerWon := teamsInMatch[targetTeamID]["won"].(bool) // 대상 플레이어 팀의 승리 여부

		// 이 경기에서 만난 상대방들과의 통계 집계
		for _, opponentObj := range playersRaw {
			opponent := opponentObj.(map[string]interface{})
			opponentPUUID := opponent["puuid"].(string)

			if opponentPUUID == targetPUUID { // 자기 자신은 스킵
				continue
			}

			// OpponentInteraction 맵에 해당 상대방 엔트리가 없으면 새로 생성
			if _, exists := opponentStats[opponentPUUID]; !exists {
				opponentStats[opponentPUUID] = &OpponentInteraction{PUUID: opponentPUUID}
			}
			currentOpponentStats := opponentStats[opponentPUUID]

			// 같은 팀인지 다른 팀인지 확인
			if opponent["teamId"].(string) != targetTeamID { // 상대방이 적 팀인 경우
				currentOpponentStats.MatchesMet++ // 적 팀으로 만난 경기 수 증가
				if targetPlayerWon {
					currentOpponentStats.WinsAgainst++
				} else {
					currentOpponentStats.LossesAgainst++
				}
				// 상대방과 만났을 때의 KDA 누적
				currentOpponentStats.TotalKDAAgainst += targetKDA

				// "상대에게 몇 번 죽었는지"는 Riot API의 Match Timeline (별도 API)이
				// 필요하므로 Match History API만으로는 정확한 구현이 어렵습니다.
				// 여기서는 이 정보를 직접 추적하지 않습니다.
			}
			// 같은 팀인 경우는 동료이므로 상호작용 통계에 집계하지 않음
		}
	}

	// 3. 집계된 통계를 기반으로 어뷰징 패턴 분석 (임계값 설정)
	const MIN_MATCHES_FOR_DETAILED_ANALYSIS = 5 // 최소 5번 이상 만나야 상세 분석 시작
	const MAX_AVG_KDA_AGAINST_OPPONENT = 0.5    // 특정 상대에게 평균 KDA가 0.5 이하면 의심 (고의 패배 가능성)
	const MIN_LOSS_RATIO_AGAINST_OPPONENT = 0.8 // 특정 상대에게 80% 이상 졌으면 의심 (고의 패배 가능성)

	for _, stats := range opponentStats {
		if stats.MatchesMet >= MIN_MATCHES_FOR_DETAILED_ANALYSIS {
			avgKDA := stats.TotalKDAAgainst / float64(stats.MatchesMet)
			lossRatio := float64(stats.LossesAgainst) / float64(stats.MatchesMet)

			// 규칙 1: 특정 상대에게 KDA가 비정상적으로 낮은 경우
			if avgKDA <= MAX_AVG_KDA_AGAINT_OPPONENT {
				suspiciousFindings = append(suspiciousFindings,
					fmt.Sprintf("상대 PUUID: %s, 대전 횟수: %d회, 플레이어 평균 KDA: %.2f (임계값 %.2f 이하로 매우 낮음 - 고의 패배 의심)",
						stats.PUUID, stats.MatchesMet, avgKDA, MAX_AVG_KDA_AGAINST_OPPONENT))
			}

			// 규칙 2: 특정 상대에게 패배율이 비정상적으로 높은 경우
			if lossRatio >= MIN_LOSS_RATIO_AGAINST_OPPONENT {
				suspiciousFindings = append(suspiciousFindings,
					fmt.Sprintf("상대 PUUID: %s, 대전 횟수: %d회, 플레이어 패배율: %.2f (임계값 %.2f 이상으로 매우 높음 - 고의 패배 의심)",
						stats.PUUID, stats.MatchesMet, lossRatio, MIN_LOSS_RATIO_AGAINST_OPPONENT))
			}
			// 추가 규칙들을 여기에 적용할 수 있습니다.
			// 예: 특정 상대와의 경기에서만 유독 킬/데스 차이가 심하다든지,
			// 특정 시간대에만 반복적으로 만난다든지 하는 패턴 분석.
		}
	}

	return suspiciousFindings, nil
}

func main() {
	// ---------------------------------------------------------------------
	// 1. 환경 변수 설정: Riot API 키 및 MongoDB URI
	// ---------------------------------------------------------------------

	// Riot API 키를 환경 변수에서 가져옵니다. (보안상 코드에 직접 입력하지 마세요!)
	// 실행 전 터미널에서 'export RIOT_API_KEY="YOUR_RIOT_API_KEY"' (Linux/macOS)
	// 또는 '$env:RIOT_API_KEY="YOUR_RIOT_API_KEY"' (PowerShell)로 설정해야 합니다.
	riotAPIKey := os.Getenv("RIOT_API_KEY")
	if riotAPIKey == "" {
		log.Fatal("오류: RIOT_API_KEY 환경 변수가 설정되지 않았습니다. API 키를 설정해주세요.")
	}

	// MongoDB URI를 환경 변수에서 가져옵니다. (로컬이면 기본값 사용 가능)
	mongoURI := os.Getenv("MONGO_URI")
	if mongoURI == "" {
		mongoURI = "mongodb://localhost:27017" // 로컬 MongoDB 기본 URI
	}

	// ---------------------------------------------------------------------
	// 2. MongoDB 연결 설정
	// ---------------------------------------------------------------------
	// Context with a timeout for MongoDB connection
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel() // context를 사용하는 함수가 종료될 때 cancel 호출

	clientOptions := options.Client().ApplyURI(mongoURI)
	mongoClient, err := mongo.Connect(ctx, clientOptions) // context 사용
	if err != nil {
		log.Fatalf("MongoDB 연결 실패: %v", err)
	}
	defer func() {
		if err = mongoClient.Disconnect(context.TODO()); err != nil { // 종료 시 context.TODO() 사용
			log.Fatalf("MongoDB 연결 해제 실패: %v", err)
		}
	}()

	// MongoDB 연결 확인 (Ping)
	err = mongoClient.Ping(ctx, readpref.Primary()) // context 사용
	if err != nil {
		log.Fatalf("MongoDB Ping 실패 (연결 문제 또는 서버 미실행): %v", err)
	}
	fmt.Println("MongoDB에 성공적으로 연결되었습니다!")

	// 사용할 데이터베이스 및 컬렉션 지정
	db := mongoClient.Database("valorant_abusing_detector")
	matchesCollection := db.Collection("matches") // 경기 상세 정보 저장
	playersCollection := db.Collection("players") // 플레이어 요약 정보 저장
	// abusingReportsCollection := db.Collection("abusing_reports") // 어뷰징 감지 결과 저장 (선택 사항)

	// ---------------------------------------------------------------------
	// 3. Riot API 클라이언트 초기화
	// ---------------------------------------------------------------------
	riotClient := NewRiotAPIClient(riotAPIKey)

	// ---------------------------------------------------------------------
	// 4. Gin 라우터(API 서버) 설정
	// ---------------------------------------------------------------------
	router := gin.Default() // Gin 라우터 인스턴스 생성 (기본 미들웨어 포함)

	// ----------------------------------------------------
	// 올바른 CORS 미들웨어 설정 (기존 CORS 코드와 교체)
	// ----------------------------------------------------
	corsConfig := cors.DefaultConfig()

	// 중요: Vercel에 배포된 프론트엔드의 실제 URL로 바꿔주세요!
	// 로컬 개발용 URL도 포함시켜 두는 것이 좋습니다.
	corsConfig.AllowedOrigins = []string{
		"http://localhost:5173", // 로컬 React 개발 서버 URL
		"https://valorant-abusing-frontend-mfvh52vve-park-yunjaes-projects.vercel.app", // **여기에 당신의 Vercel 프론트엔드 URL을 입력하세요!**
		// 예시: "https://valorant-abusing-detector-frontend.vercel.app"
	}//Test
	corsConfig.AllowMethods = []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"}
	corsConfig.AllowHeaders = []string{"Origin", "Content-Type", "Accept", "Authorization"} // 필요한 헤더 추가 (Authorization 같은 헤더도 추가해두면 좋습니다)
	corsConfig.AllowCredentials = true // 쿠키/인증 정보 전송 허용
	corsConfig.MaxAge = 300 // 5분 (프리플라이트 요청 캐싱 시간)

	router.Use(cors.New(corsConfig)) // Gin에 CORS 미들웨어 적용
	// ----------------------------------------------------


	// Health Check 엔드포인트
	router.GET("/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": "pong"})
	})

	// Riot ID로 플레이어 계정 정보를 가져오는 예시 API 엔드포인트
	// 예시 URL: http://localhost:8080/account/riotid?gameName=플레이어이름&tagLine=태그라인
	router.GET("/account/riotid", func(c *gin.Context) {
		gameName := c.Query("gameName") // 쿼리 파라미터에서 gameName 가져오기
		tagLine := c.Query("tagLine")   // 쿼리 파라미터에서 tagLine 가져오기

		if gameName == "" || tagLine == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "gameName과 tagLine을 모두 입력해야 합니다."})
			return
		}

		account, err := riotClient.GetAccountByRiotID(gameName, tagLine)
		if err != nil {
			log.Printf("Riot API 호출 실패: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Riot API 호출 중 오류 발생: %v", err)})
			return
		}

		// 가져온 계정 정보를 playersCollection에 저장 (또는 업데이트)
		// PUUID를 기준으로 upsert(업데이트 또는 삽입)를 고려할 수 있습니다.
		_, err = playersCollection.InsertOne(context.TODO(), account)
		if err != nil {
			log.Printf("플레이어 계정 정보 MongoDB 저장 실패: %v", err)
		} else {
			fmt.Println("플레이어 계정 정보 MongoDB에 저장됨:", account["puuid"])
		}

		c.JSON(http.StatusOK, account) // 가져온 계정 정보를 JSON으로 반환
	})

	// ---------------------------------------------------------------------
	// 새로운 엔드포인트: 플레이어의 경기 데이터 수집 및 저장 + 어뷰징 감지
	// 예시 URL: http://localhost:8080/player/matches/YOUR_PUUID?count=20
	// ---------------------------------------------------------------------
	router.GET("/player/matches/:puuid", func(c *gin.Context) {
		puuid := c.Param("puuid")           // URL 경로 파라미터에서 PUUID 가져오기
		countStr := c.DefaultQuery("count", "5") // 쿼리 파라미터에서 가져올 경기 수 (기본값 5)

		count, err := strconv.Atoi(countStr)
		if err != nil || count <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "유효한 경기 수(count)를 입력해야 합니다."})
			return
		}
		if count > 20 { // 개발자 키 제한 고려 (최대 20개 가져오는 것이 안전)
			count = 20
			log.Println("경기 수(count)를 20개로 제한합니다 (라이엇 개발자 키 Rate Limit 고려).")
		}

		// 1. PUUID로 경기 ID 목록 가져오기
		matchIDs, err := riotClient.GetMatchListByPUUID(puuid, count)
		if err != nil {
			log.Printf("경기 ID 목록 가져오기 실패: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("경기 ID 목록 가져오기 실패: %v", err)})
			return
		}

		if len(matchIDs) == 0 {
			c.JSON(http.StatusOK, gin.H{"message": "해당 플레이어의 최근 경기를 찾을 수 없습니다.", "processedMatches": 0})
			return
		}

		processedCount := 0

		// 2. 각 경기 상세 정보 가져오기 및 MongoDB에 저장
		// (주의: 현재는 순차적으로 처리하지만, 많은 경기를 처리할 때는 Go루틴을 이용한 동시 처리 고려)
		for _, matchID := range matchIDs {
			// MongoDB에 이미 해당 매치 ID가 있는지 확인하여 중복 저장 방지
			filter := map[string]string{"metadata.matchId": matchID}
			var existingMatch map[string]interface{}
			err := matchesCollection.FindOne(context.TODO(), filter).Decode(&existingMatch)

			if err == nil { // 이미 존재하는 경우
				log.Printf("매치 ID %s는 이미 MongoDB에 존재합니다. 건너뜁니다.", matchID)
				processedCount++ // 이미 존재하는 것도 처리된 것으로 간주
				continue
			} else if err != mongo.ErrNoDocuments { // 문서가 없어서 발생한 에러가 아닌 다른 DB 에러인 경우
				log.Printf("매치 ID %s 확인 중 MongoDB 오류 발생: %v", matchID, err)
				continue
			}

			// 존재하지 않으면 Riot API에서 상세 정보 가져오기
			matchDetails, err := riotClient.GetMatchDetails(matchID)
			if err != nil {
				log.Printf("매치 ID %s의 상세 정보 가져오기 실패: %v", matchID, err)
				continue // 실패해도 다른 경기 계속 처리
			}

			// MongoDB에 저장
			_, err = matchesCollection.InsertOne(context.TODO(), matchDetails)
			if err != nil {
				log.Printf("매치 ID %s의 MongoDB 저장 실패: %v", matchID, err)
				continue
			}
			processedCount++
			log.Printf("매치 ID %s가 MongoDB에 성공적으로 저장되었습니다.", matchID)
		}

		// 3. 경기 데이터 저장 완료 후, 어뷰징 감지 로직 실행 (전체 데이터 기반)
		abusingFindings, analyzeErr := CheckForAbusing(matchesCollection, puuid)
		if analyzeErr != nil {
			log.Printf("어뷰징 감지 중 오류 발생: %v", analyzeErr)
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("어뷰징 감지 중 오류 발생: %v", analyzeErr)})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"message":          fmt.Sprintf("플레이어 %s의 %d개 경기 데이터 수집 완료 및 저장 시도.", puuid, len(matchIDs)),
			"requestedMatches": len(matchIDs),
			"processedMatches": processedCount,
			"abusingDetected":  len(abusingFindings) > 0, // 어뷰징 감지 여부 (발견된 내용이 있으면 true)
			"abusingDetails":   abusingFindings,          // 어뷰징 상세 내용 목록
		})
	})

	// ---------------------------------------------------------------------
	// 5. 서버 실행
	// ---------------------------------------------------------------------
	// 환경 변수에서 PORT를 가져오거나, 없으면 기본 8080 포트 사용
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	log.Printf("서버가 :%s 포트에서 시작됩니다.", port)
	if err := router.Run(":" + port); err != nil {
		log.Fatalf("서버 실행 실패: %v", err)
	}
}