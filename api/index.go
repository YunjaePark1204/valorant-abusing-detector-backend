package handler

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/YunjaePark1204/valorant-abusing-detector-backend/pkg/detector"
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

	memMatchMu    sync.RWMutex
	memMatchCache = make(map[string][]map[string]interface{})
	memMatchOrder []string
)

func loadDotEnvIfPresent(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.IndexByte(line, '=')
		if idx <= 0 {
			continue
		}
		k := strings.TrimSpace(line[:idx])
		v := strings.Trim(strings.TrimSpace(line[idx+1:]), `"'`)
		if os.Getenv(k) == "" {
			_ = os.Setenv(k, v)
		}
	}
}

func init() {
	loadDotEnvIfPresent(".env")
	mongoURI := os.Getenv("MONGO_URI")
	if mongoURI != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		clientOptions := options.Client().ApplyURI(mongoURI)
		if client, err := mongo.Connect(ctx, clientOptions); err == nil {
			if pingErr := client.Ping(ctx, nil); pingErr == nil {
				mongoClient = client
			} else {
				log.Printf("MongoDB ping error: %v", pingErr)
			}
		} else {
			log.Printf("MongoDB connect error: %v", err)
		}
	}

	henrikAPIKey = os.Getenv("HENRIK_API_KEY")
	httpClient = &http.Client{Timeout: 6 * time.Second}

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	r.GET("/api/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": "pong", "dbConnected": mongoClient != nil})
	})
	r.GET("/api/account/riotid", getAccount)
	r.GET("/api/player/matches/:puuid", getMatches)

	router = r
}

func isAllowedOrigin(origin string) bool {
	if origin == "" {
		return false
	}
	if origin == "https://valorant-abusing-frontend.vercel.app" ||
		strings.HasPrefix(origin, "http://localhost:") ||
		strings.HasPrefix(origin, "http://127.0.0.1:") {
		return true
	}
	if strings.HasPrefix(origin, "https://") && strings.HasSuffix(origin, ".vercel.app") {
		return true
	}
	for _, extra := range strings.Split(os.Getenv("ALLOWED_ORIGINS"), ",") {
		if strings.TrimSpace(extra) == origin {
			return true
		}
	}
	return false
}

func Handler(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if isAllowedOrigin(origin) {
		w.Header().Set("Access-Control-Allow-Origin", origin)
	} else {
		w.Header().Set("Access-Control-Allow-Origin", "https://valorant-abusing-frontend.vercel.app")
	}
	w.Header().Set("Vary", "Origin")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS, PUT, DELETE")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	w.Header().Set("Access-Control-Allow-Credentials", "true")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	router.ServeHTTP(w, r)
}

func getAccount(c *gin.Context) {
	name := strings.TrimSpace(c.Query("gameName"))
	tag := strings.TrimSpace(c.Query("tagLine"))
	if name == "" || tag == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "gameName과 tagLine이 필요합니다."})
		return
	}

	nameLower := strings.ToLower(name)
	tagLower := strings.ToLower(tag)
	now := time.Now().Unix()
	const cacheTTLSeconds = 6 * 3600

	if mongoClient != nil {
		col := mongoClient.Database("valorant").Collection("playerAccounts")
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		var cached map[string]interface{}
		filter := bson.M{
			"$or": []bson.M{
				{"nameLower": nameLower, "tagLower": tagLower},
				{"name": name, "tag": tag},
			},
		}
		if err := col.FindOne(ctx, filter).Decode(&cached); err == nil {
			updatedAt, _ := cached["updatedAt"].(int64)
			if updatedAt == 0 {
				if f, ok := cached["updatedAt"].(float64); ok {
					updatedAt = int64(f)
				}
			}
			if updatedAt > 0 && (now-updatedAt) < cacheTTLSeconds {
				c.JSON(http.StatusOK, cached)
				return
			}
		}
	}

	apiURL := fmt.Sprintf(
		"https://api.henrikdev.xyz/valorant/v1/account/%s/%s",
		url.PathEscape(name),
		url.PathEscape(tag),
	)
	req, err := http.NewRequest(http.MethodGet, apiURL, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "요청 생성 중 오류가 발생했습니다."})
		return
	}
	if henrikAPIKey != "" {
		req.Header.Add("Authorization", henrikAPIKey)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "라이엇/HenrikDev 서버와 통신할 수 없습니다."})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		c.JSON(resp.StatusCode, gin.H{"error": "HenrikDev API 키가 누락되었거나 유효하지 않습니다. (백엔드 .env의 HENRIK_API_KEY를 확인해주세요)"})
		return
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "API 요청 제한(Rate Limit)에 걸렸습니다. 잠시 후 다시 시도해주세요."})
		return
	}
	if resp.StatusCode == http.StatusNotFound {
		c.JSON(http.StatusNotFound, gin.H{"error": "플레이어를 찾을 수 없습니다. 닉네임과 태그를 다시 확인해주세요."})
		return
	}
	if resp.StatusCode != http.StatusOK {
		c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("계정 조회에 실패했습니다. (상태 코드: %d)", resp.StatusCode)})
		return
	}

	var apiRes map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&apiRes); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "계정 응답 데이터 파싱에 실패했습니다."})
		return
	}
	userData, ok := apiRes["data"].(map[string]interface{})
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "플레이어 데이터를 찾을 수 없습니다."})
		return
	}

	if mongoClient != nil {
		if puuid, ok := userData["puuid"].(string); ok && puuid != "" {
			if actualName, ok := userData["name"].(string); ok && actualName != "" {
				userData["nameLower"] = strings.ToLower(actualName)
			} else {
				userData["nameLower"] = nameLower
			}
			if actualTag, ok := userData["tag"].(string); ok && actualTag != "" {
				userData["tagLower"] = strings.ToLower(actualTag)
			} else {
				userData["tagLower"] = tagLower
			}
			userData["updatedAt"] = now

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			col := mongoClient.Database("valorant").Collection("playerAccounts")
			_, _ = col.UpdateOne(
				ctx,
				bson.M{"puuid": puuid},
				bson.M{"$set": userData},
				options.Update().SetUpsert(true),
			)
		}
	}

	c.JSON(http.StatusOK, userData)
}

func fetchHenrikPage(urlStr string) ([]map[string]interface{}, int, error) {
	req, err := http.NewRequest(http.MethodGet, urlStr, nil)
	if err != nil {
		return nil, 0, err
	}
	if henrikAPIKey != "" {
		req.Header.Add("Authorization", henrikAPIKey)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, fmt.Errorf("upstream status %d", resp.StatusCode)
	}

	var res struct {
		Data []map[string]interface{} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, resp.StatusCode, err
	}
	return res.Data, resp.StatusCode, nil
}

func getMatches(c *gin.Context) {
	puuid := strings.TrimSpace(c.Param("puuid"))
	if puuid == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "puuid가 필요합니다."})
		return
	}

	region := strings.ToLower(c.DefaultQuery("region", "kr"))
	mode := strings.ToLower(strings.TrimSpace(c.DefaultQuery("mode", "competitive")))
	if mode == "" {
		mode = "competitive"
	}

	start, _ := strconv.Atoi(c.DefaultQuery("start", "0"))
	if start < 0 {
		start = 0
	}
	pages, _ := strconv.Atoi(c.DefaultQuery("pages", "2"))
	if pages < 1 {
		pages = 1
	} else if pages > 3 {
		pages = 3
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	if limit < 10 {
		limit = 10
	} else if limit > 200 {
		limit = 200
	}

	const pageSize = 10

	// If DB is not connected and memory cache has no prior matches for this player on a paginated load,
	// backfill earlier pages [0..start) so all matches are analyzed together.
	if mongoClient == nil && start > 0 && len(getCachedMatches(puuid)) == 0 {
		backfillPages := start / pageSize
		if backfillPages > 4 {
			backfillPages = 4
		}
		if backfillPages > 0 {
			bfResults := fetchPagesConcurrently(region, puuid, mode, 0, backfillPages, pageSize)
			var bfMatches []map[string]interface{}
			for _, r := range bfResults {
				if r.err == nil && len(r.matches) > 0 {
					bfMatches = append(bfMatches, r.matches...)
				}
			}
			if len(bfMatches) > 0 {
				upsertCachedMatches(puuid, bfMatches)
			}
		}
	}

	results := fetchPagesConcurrently(region, puuid, mode, start, pages, pageSize)

	var newMatches []map[string]interface{}
	rateLimited := false
	lastPageCount := 0

	for i, r := range results {
		if r.status == http.StatusTooManyRequests {
			rateLimited = true
		}
		if r.err == nil && len(r.matches) > 0 {
			newMatches = append(newMatches, r.matches...)
			if i == pages-1 {
				lastPageCount = len(r.matches)
			}
		}
	}

	// Fallback to v3 if v4 failed completely on initial load
	if len(newMatches) == 0 && start == 0 && !rateLimited {
		v3URL := fmt.Sprintf(
			"https://api.henrikdev.xyz/valorant/v3/by-puuid/matches/%s/%s?size=%d",
			url.PathEscape(region),
			url.PathEscape(puuid),
			pageSize,
		)
		if mode != "all" {
			v3URL += "&mode=" + url.QueryEscape(mode)
		}
		if m, status, err := fetchHenrikPage(v3URL); err == nil {
			newMatches = append(newMatches, m...)
			lastPageCount = len(m)
		} else if status == http.StatusTooManyRequests {
			rateLimited = true
		}
	}

	if len(newMatches) > 0 {
		upsertCachedMatches(puuid, newMatches)
	}

	var finalMatches []map[string]interface{}
	if mongoClient != nil {
		col := mongoClient.Database("valorant").Collection("matches")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if len(newMatches) > 0 {
			writeModels := make([]mongo.WriteModel, 0, len(newMatches))
			for _, m := range newMatches {
				matchID, modeID, gameStart, puuids := detector.ExtractMatchMeta(m)
				if matchID == "" {
					continue
				}
				doc := make(bson.M, len(m)+3)
				for k, v := range m {
					doc[k] = v
				}
				doc["_mode_id"] = modeID
				doc["_game_start"] = gameStart
				doc["_puuids"] = puuids

				filter := bson.M{
					"$or": []bson.M{
						{"metadata.matchid": matchID},
						{"metadata.match_id": matchID},
					},
				}
				model := mongo.NewUpdateOneModel().
					SetFilter(filter).
					SetUpdate(bson.M{"$set": doc}).
					SetUpsert(true)
				writeModels = append(writeModels, model)
			}
			if len(writeModels) > 0 {
				_, _ = col.BulkWrite(ctx, writeModels, options.BulkWrite().SetOrdered(false))
			}
		}

		queryFilter := bson.M{
			"$or": []bson.M{
				{"_puuids": puuid},
				{"players.all_players.puuid": puuid},
				{"players.puuid": puuid},
			},
		}
		if mode != "all" {
			queryFilter = bson.M{
				"$and": []bson.M{
					queryFilter,
					{
						"$or": []bson.M{
							{"_mode_id": mode},
							{"metadata.mode_id": mode},
							{"metadata.queue.id": mode},
							{"metadata.mode": primitive.Regex{Pattern: "^" + mode + "$", Options: "i"}},
						},
					},
				},
			}
		}

		findOpts := options.Find().
			SetSort(bson.D{{Key: "_game_start", Value: -1}, {Key: "metadata.game_start", Value: -1}}).
			SetLimit(int64(limit)).
			SetProjection(bson.M{"rounds": 0, "kills": 0})

		cursor, err := col.Find(ctx, queryFilter, findOpts)
		if err == nil {
			var dbMatches []map[string]interface{}
			if err = cursor.All(ctx, &dbMatches); err == nil && len(dbMatches) > 0 {
				finalMatches = mergeUniqueMatches(dbMatches, getCachedMatches(puuid))
			}
		}
	}

	if len(finalMatches) == 0 {
		finalMatches = getCachedMatches(puuid)
	}
	if len(finalMatches) == 0 {
		finalMatches = newMatches
	}

	analysis := detector.AnalyzeMatches(finalMatches, puuid, mode)

	warning := ""
	if rateLimited {
		if analysis.MatchesCount > 0 {
			warning = "API 요청 제한(Rate Limit)으로 최신 매치를 일부 가져오지 못해 저장된 전적 기준으로 분석했습니다."
		} else {
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "API 요청 제한(Rate Limit)에 걸렸습니다. 잠시 후 다시 시도해주세요."})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"matchesCount":    analysis.MatchesCount,
		"modeFilter":      analysis.ModeFilter,
		"abusingDetected": analysis.AbusingDetected,
		"summary":         analysis.Summary,
		"details":         analysis.Details,
		"players":         analysis.Players,
		"history":         analysis.History,
		"fetchedNewCount": len(newMatches),
		"nextStart":       start + pages*pageSize,
		"hasMore":         lastPageCount >= pageSize || len(newMatches) >= pages*pageSize,
		"rateLimited":     rateLimited,
		"warning":         warning,
	})
}

type pageResult struct {
	matches []map[string]interface{}
	status  int
	err     error
}

func fetchPagesConcurrently(region, puuid, mode string, start, pages, pageSize int) []pageResult {
	results := make([]pageResult, pages)
	var wg sync.WaitGroup
	for i := 0; i < pages; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			offset := start + idx*pageSize
			u := fmt.Sprintf(
				"https://api.henrikdev.xyz/valorant/v4/by-puuid/matches/%s/pc/%s?size=%d&start=%d",
				url.PathEscape(region),
				url.PathEscape(puuid),
				pageSize,
				offset,
			)
			if mode != "all" {
				u += "&mode=" + url.QueryEscape(mode)
			}
			m, status, err := fetchHenrikPage(u)
			results[idx] = pageResult{matches: m, status: status, err: err}
		}(i)
	}
	wg.Wait()
	return results
}

func upsertCachedMatches(puuid string, matches []map[string]interface{}) {
	if puuid == "" || len(matches) == 0 {
		return
	}
	key := strings.ToLower(puuid)
	memMatchMu.Lock()
	defer memMatchMu.Unlock()

	existing, exists := memMatchCache[key]
	if !exists {
		if len(memMatchOrder) >= 100 {
			oldest := memMatchOrder[0]
			memMatchOrder = memMatchOrder[1:]
			delete(memMatchCache, oldest)
		}
		memMatchOrder = append(memMatchOrder, key)
	}
	merged := mergeUniqueMatches(existing, matches)
	if len(merged) > 200 {
		merged = merged[:200]
	}
	memMatchCache[key] = merged
}

func getCachedMatches(puuid string) []map[string]interface{} {
	key := strings.ToLower(puuid)
	memMatchMu.RLock()
	defer memMatchMu.RUnlock()
	list := memMatchCache[key]
	out := make([]map[string]interface{}, len(list))
	copy(out, list)
	return out
}

func mergeUniqueMatches(primary, extra []map[string]interface{}) []map[string]interface{} {
	seen := make(map[string]struct{}, len(primary)+len(extra))
	merged := make([]map[string]interface{}, 0, len(primary)+len(extra))
	for _, m := range primary {
		id, _, _, _ := detector.ExtractMatchMeta(m)
		if id != "" {
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
		}
		merged = append(merged, m)
	}
	for _, m := range extra {
		id, _, _, _ := detector.ExtractMatchMeta(m)
		if id != "" {
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
		}
		merged = append(merged, m)
	}
	return merged
}
