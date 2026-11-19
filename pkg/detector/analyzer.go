package detector

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// MatchParticipant represents a single player's performance in one match.
type MatchParticipant struct {
	PUUID     string  `json:"puuid"`
	Name      string  `json:"name"`
	Tag       string  `json:"tag"`
	FullName  string  `json:"fullName"`
	Team      string  `json:"team"`
	IsTarget  bool    `json:"isTarget"`
	IsAlly    bool    `json:"isAlly"`
	SameParty bool    `json:"sameParty"`
	PartyID   string  `json:"partyId,omitempty"`
	Agent     string  `json:"agent"`
	AgentID   string  `json:"agentId,omitempty"`
	Tier      int     `json:"tier"`
	TierName  string  `json:"tierName"`
	Kills     int     `json:"kills"`
	Deaths    int     `json:"deaths"`
	Assists   int     `json:"assists"`
	Score     int     `json:"score"`
	ACS       float64 `json:"acs"`
	Damage    int     `json:"damage"`
	MetCount  int     `json:"metCount"`
}

// MatchSummary summarizes a single match and its 10 participants.
type MatchSummary struct {
	MatchID      string             `json:"matchId"`
	Map          string             `json:"map"`
	Mode         string             `json:"mode"`
	ModeID       string             `json:"modeId"`
	GameStart    int64              `json:"gameStart"`
	GameLength   int64              `json:"gameLength"`
	RoundsPlayed int                `json:"roundsPlayed"`
	RoundsWon    int                `json:"roundsWon"`
	RoundsLost   int                `json:"roundsLost"`
	Agent        string             `json:"agent"`
	AgentID      string             `json:"agentId,omitempty"`
	Tier         int                `json:"tier"`
	TierName     string             `json:"tierName"`
	Result       string             `json:"result"`
	Kills        int                `json:"kills"`
	Deaths       int                `json:"deaths"`
	Assists      int                `json:"assists"`
	Score        int                `json:"score"`
	ACS          float64            `json:"acs"`
	RepeatCount  int                `json:"repeatCount"`
	SuspectCount int                `json:"suspectCount"`
	Participants []MatchParticipant `json:"participants"`
}

// PlayerEncounter describes a specific match where the target player met another player.
type PlayerEncounter struct {
	MatchID      string  `json:"matchId"`
	Map          string  `json:"map"`
	Mode         string  `json:"mode"`
	GameStart    int64   `json:"gameStart"`
	RoundsWon    int     `json:"roundsWon"`
	RoundsLost   int     `json:"roundsLost"`
	TargetResult string  `json:"targetResult"`
	Relation     string  `json:"relation"` // "아군" or "적군"
	SameParty    bool    `json:"sameParty"`
	TheirAgent   string  `json:"theirAgent"`
	TheirAgentID string  `json:"theirAgentId,omitempty"`
	TheirTier    string  `json:"theirTier"`
	TheirKills   int     `json:"theirKills"`
	TheirDeaths  int     `json:"theirDeaths"`
	TheirAssists int     `json:"theirAssists"`
	TheirACS     float64 `json:"theirAcs"`
	TargetAgent  string  `json:"targetAgent"`
	TargetKDA    string  `json:"targetKda"`
}

// PlayerStat aggregates all encounters with a specific player across analyzed matches.
type PlayerStat struct {
	PUUID             string            `json:"puuid"`
	Name              string            `json:"name"`
	GameName          string            `json:"gameName"`
	TagLine           string            `json:"tagLine"`
	LatestTier        string            `json:"latestTier"`
	Met               int               `json:"met"`
	SoloMet           int               `json:"soloMet"`
	SamePartyCount    int               `json:"samePartyCount"`
	AsAlly            int               `json:"asAlly"`
	SoloAsAlly        int               `json:"soloAsAlly"`
	SoloAllyLost      int               `json:"soloAllyLost"`
	AllyWon           int               `json:"allyWon"`
	AllyLost          int               `json:"allyLost"`
	AllyDraw          int               `json:"allyDraw"`
	AsEnemy           int               `json:"asEnemy"`
	EnemyWon          int               `json:"enemyWon"`
	EnemyLost         int               `json:"enemyLost"`
	EnemyDraw         int               `json:"enemyDraw"`
	TargetLost        int               `json:"targetLost"`
	Kills             float64           `json:"kills"`
	Deaths            float64           `json:"deaths"`
	Assists           float64           `json:"assists"`
	Score             float64           `json:"score"`
	AvgACS            float64           `json:"avgAcs"`
	ACSAsAlly         float64           `json:"acsAsAlly"`
	ACSAsEnemy        float64           `json:"acsAsEnemy"`
	QuickRequeueCount int               `json:"quickRequeueCount"`
	SuspicionScore    int               `json:"suspicionScore"`
	SuspicionLevel    string            `json:"suspicionLevel"` // "none", "watch", "high"
	SuspicionReasons  []string          `json:"suspicionReasons"`
	Encounters        []PlayerEncounter `json:"encounters"`
}

// OverallSummary provides aggregate performance stats for the analyzed match set.
type OverallSummary struct {
	TotalMatches       int     `json:"totalMatches"`
	Wins               int     `json:"wins"`
	Losses             int     `json:"losses"`
	Draws              int     `json:"draws"`
	WinRate            float64 `json:"winRate"`
	AvgKills           float64 `json:"avgKills"`
	AvgDeaths          float64 `json:"avgDeaths"`
	AvgAssists         float64 `json:"avgAssists"`
	KDA                float64 `json:"kda"`
	AvgACS             float64 `json:"avgAcs"`
	RepeatPlayersCount int     `json:"repeatPlayersCount"`
	SuspiciousCount    int     `json:"suspiciousCount"`
}

// AnalysisResult is the complete payload returned by AnalyzeMatches.
type AnalysisResult struct {
	MatchesCount    int            `json:"matchesCount"`
	ModeFilter      string         `json:"modeFilter"`
	AbusingDetected bool           `json:"abusingDetected"`
	Summary         OverallSummary `json:"summary"`
	Details         []string       `json:"details"`
	Players         []PlayerStat   `json:"players"`
	History         []MatchSummary `json:"history"`
}

// ParsedMatch is a version-agnostic representation of a HenrikDev v3 or v4 match document.
type ParsedMatch struct {
	MatchID      string
	Map          string
	Mode         string
	ModeID       string
	GameStart    int64
	GameLength   int64
	RoundsPlayed int
	Teams        map[string]ParsedTeam
	Players      []ParsedPlayer
}

// ParsedTeam holds team win/round counts.
type ParsedTeam struct {
	HasWon     bool
	RoundsWon  int
	RoundsLost int
}

// ParsedPlayer holds normalized participant fields.
type ParsedPlayer struct {
	PUUID    string
	Name     string
	Tag      string
	Team     string
	PartyID  string
	Agent    string
	AgentID  string
	Tier     int
	TierName string
	Kills    int
	Deaths   int
	Assists  int
	Score    int
	Damage   int
}

// ExtractMatchMeta extracts matchId, normalized modeId, gameStart unix seconds, and player PUUIDs for DB indexing.
func ExtractMatchMeta(raw map[string]interface{}) (matchID, modeID string, gameStart int64, puuids []string) {
	pm := ParseMatch(raw)
	if pm == nil {
		return "", "", 0, nil
	}
	seen := make(map[string]struct{}, len(pm.Players))
	for _, p := range pm.Players {
		if p.PUUID == "" {
			continue
		}
		if _, ok := seen[p.PUUID]; !ok {
			seen[p.PUUID] = struct{}{}
			puuids = append(puuids, p.PUUID)
		}
	}
	return pm.MatchID, pm.ModeID, pm.GameStart, puuids
}

// ParseMatch normalizes both HenrikDev v3 and v4 match JSON maps into a unified ParsedMatch.
func ParseMatch(raw map[string]interface{}) *ParsedMatch {
	if raw == nil {
		return nil
	}
	meta, ok := asMap(raw["metadata"])
	if !ok {
		return nil
	}

	matchID := firstNonEmpty(asString(meta["matchid"]), asString(meta["match_id"]))
	if matchID == "" {
		return nil
	}

	mapName := asString(meta["map"])
	if mapName == "" {
		if mapObj, ok := asMap(meta["map"]); ok {
			mapName = asString(mapObj["name"])
		}
	}

	modeName, modeID := parseMode(meta)
	gameStart := parseGameStart(meta)
	gameLength := parseGameLength(meta)
	roundsPlayed := asInt(meta["rounds_played"])

	teams := parseTeams(raw["teams"])
	if roundsPlayed == 0 {
		if red, ok := teams["red"]; ok {
			roundsPlayed = red.RoundsWon + red.RoundsLost
		} else if blue, ok := teams["blue"]; ok {
			roundsPlayed = blue.RoundsWon + blue.RoundsLost
		}
	}

	players := parsePlayers(raw["players"])
	if len(players) == 0 {
		return nil
	}

	return &ParsedMatch{
		MatchID:      matchID,
		Map:          mapName,
		Mode:         modeName,
		ModeID:       modeID,
		GameStart:    gameStart,
		GameLength:   gameLength,
		RoundsPlayed: roundsPlayed,
		Teams:        teams,
		Players:      players,
	}
}

func parseMode(meta map[string]interface{}) (modeName string, modeID string) {
	if qObj, ok := asMap(meta["queue"]); ok {
		modeName = asString(qObj["name"])
		modeID = strings.ToLower(asString(qObj["id"]))
	} else {
		qStr := asString(meta["queue"])
		if modeName == "" && qStr != "" {
			modeName = qStr
		}
	}

	if m := asString(meta["mode"]); m != "" {
		modeName = m
	}
	if mid := asString(meta["mode_id"]); mid != "" {
		modeID = strings.ToLower(mid)
	}

	if modeID == "" && modeName != "" {
		modeID = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(modeName), " ", "_"))
	}
	if modeName == "" && modeID != "" {
		modeName = strings.ToUpper(modeID[:1]) + modeID[1:]
	}
	return modeName, modeID
}

func parseGameStart(meta map[string]interface{}) int64 {
	if ts := asInt64(meta["game_start"]); ts > 0 {
		return ts
	}
	if iso := asString(meta["started_at"]); iso != "" {
		if t, err := time.Parse(time.RFC3339, iso); err == nil {
			return t.Unix()
		}
		if t, err := time.Parse(time.RFC3339Nano, iso); err == nil {
			return t.Unix()
		}
	}
	if iso := asString(meta["game_start_patched"]); iso != "" {
		if t, err := time.Parse(time.RFC3339, iso); err == nil {
			return t.Unix()
		}
	}
	return 0
}

func parseGameLength(meta map[string]interface{}) int64 {
	if ms := asInt64(meta["game_length_in_ms"]); ms > 0 {
		return ms / 1000
	}
	return asInt64(meta["game_length"])
}

func parseTeams(rawTeams interface{}) map[string]ParsedTeam {
	out := make(map[string]ParsedTeam)
	if teamMap, ok := asMap(rawTeams); ok {
		for k, v := range teamMap {
			tObj, ok := asMap(v)
			if !ok {
				continue
			}
			out[strings.ToLower(k)] = ParsedTeam{
				HasWon:     asBool(tObj["has_won"]) || asBool(tObj["won"]),
				RoundsWon:  asInt(tObj["rounds_won"]),
				RoundsLost: asInt(tObj["rounds_lost"]),
			}
		}
		return out
	}

	if teamArr, ok := asSlice(rawTeams); ok {
		for _, item := range teamArr {
			tObj, ok := asMap(item)
			if !ok {
				continue
			}
			tid := strings.ToLower(firstNonEmpty(asString(tObj["team_id"]), asString(tObj["team"])))
			if tid == "" {
				continue
			}
			rw, rl := asInt(tObj["rounds_won"]), asInt(tObj["rounds_lost"])
			if roundsObj, ok := asMap(tObj["rounds"]); ok {
				rw = asInt(roundsObj["won"])
				rl = asInt(roundsObj["lost"])
			}
			out[tid] = ParsedTeam{
				HasWon:     asBool(tObj["won"]) || asBool(tObj["has_won"]),
				RoundsWon:  rw,
				RoundsLost: rl,
			}
		}
	}
	return out
}

func parsePlayers(rawPlayers interface{}) []ParsedPlayer {
	var rawList []interface{}
	if pObj, ok := asMap(rawPlayers); ok {
		if all, ok := asSlice(pObj["all_players"]); ok {
			rawList = all
		}
	} else if pArr, ok := asSlice(rawPlayers); ok {
		rawList = pArr
	}

	out := make([]ParsedPlayer, 0, len(rawList))
	for _, item := range rawList {
		pMap, ok := asMap(item)
		if !ok {
			continue
		}
		puuid := asString(pMap["puuid"])
		if puuid == "" {
			continue
		}

		agentName := asString(pMap["character"])
		agentID := ""
		if agentObj, ok := asMap(pMap["agent"]); ok {
			agentName = firstNonEmpty(asString(agentObj["name"]), agentName)
			agentID = asString(agentObj["id"])
		} else if agentStr := asString(pMap["agent"]); agentStr != "" && agentName == "" {
			agentName = agentStr
		}

		tier := asInt(pMap["currenttier"])
		tierName := asString(pMap["currenttier_patched"])
		if tierObj, ok := asMap(pMap["tier"]); ok {
			if t := asInt(tierObj["id"]); t > 0 {
				tier = t
			}
			if tn := asString(tierObj["name"]); tn != "" {
				tierName = tn
			}
		}
		if tierName == "" {
			tierName = "Unranked"
		}

		var kills, deaths, assists, score, damage int
		damage = asInt(pMap["damage_made"])
		if stats, ok := asMap(pMap["stats"]); ok {
			kills = asInt(stats["kills"])
			deaths = asInt(stats["deaths"])
			assists = asInt(stats["assists"])
			score = asInt(stats["score"])
			if dmgObj, ok := asMap(stats["damage"]); ok {
				if dealt := asInt(dmgObj["dealt"]); dealt > 0 {
					damage = dealt
				}
			}
		}

		out = append(out, ParsedPlayer{
			PUUID:    puuid,
			Name:     asString(pMap["name"]),
			Tag:      asString(pMap["tag"]),
			Team:     firstNonEmpty(asString(pMap["team"]), asString(pMap["team_id"])),
			PartyID:  asString(pMap["party_id"]),
			Agent:    agentName,
			AgentID:  agentID,
			Tier:     tier,
			TierName: tierName,
			Kills:    kills,
			Deaths:   deaths,
			Assists:  assists,
			Score:    score,
			Damage:   damage,
		})
	}
	return out
}

// IsExcludedFromAbusing returns true for FFA/non-team modes such as Deathmatch where Blue/Red team win-loss does not apply.
func IsExcludedFromAbusing(modeID, modeName string) bool {
	m := strings.ToLower(firstNonEmpty(modeID, modeName))
	switch {
	case strings.Contains(m, "deathmatch") && !strings.Contains(m, "team"):
		return true
	case strings.Contains(m, "escalation"), strings.Contains(m, "snowball"), strings.Contains(m, "custom"), strings.Contains(m, "newmap"):
		return true
	}
	return false
}

// AnalyzeMatches parses raw match documents, filters by mode, detects repeat/suspicious encounters, and returns rich summaries.
func AnalyzeMatches(rawMatches []map[string]interface{}, targetPUUID string, modeFilter string) AnalysisResult {
	modeFilter = strings.ToLower(strings.TrimSpace(modeFilter))
	if modeFilter == "" {
		modeFilter = "competitive"
	}

	parsedList := make([]*ParsedMatch, 0, len(rawMatches))
	seenMatches := make(map[string]struct{}, len(rawMatches))

	for _, raw := range rawMatches {
		pm := ParseMatch(raw)
		if pm == nil {
			continue
		}
		if _, dup := seenMatches[pm.MatchID]; dup {
			continue
		}
		seenMatches[pm.MatchID] = struct{}{}

		if modeFilter != "all" {
			if !strings.EqualFold(pm.ModeID, modeFilter) && !strings.EqualFold(pm.Mode, modeFilter) {
				continue
			}
		}
		// Never pollute abusing or tracker team analysis with FFA Deathmatch unless explicitly inspected in history?
		// When modeFilter == "all", still skip pure Deathmatch from team-based abusing stats.
		parsedList = append(parsedList, pm)
	}

	// Sort newest first so latest names/tiers and chronological encounters are accurate.
	sort.Slice(parsedList, func(i, j int) bool {
		return parsedList[i].GameStart > parsedList[j].GameStart
	})

	statsMap := make(map[string]*PlayerStat)
	histories := make([]MatchSummary, 0, len(parsedList))

	var totalWins, totalLosses, totalDraws int
	var totalKills, totalDeaths, totalAssists int
	var totalACS float64
	var acsSamples int

	for _, pm := range parsedList {
		var targetPlayer *ParsedPlayer
		for i := range pm.Players {
			if strings.EqualFold(pm.Players[i].PUUID, targetPUUID) {
				targetPlayer = &pm.Players[i]
				break
			}
		}
		if targetPlayer == nil {
			continue
		}

		teamKey := strings.ToLower(targetPlayer.Team)
		targetWon := false
		isDraw := false
		resultStr := "-"
		var roundsWon, roundsLost int

		if teamInfo, ok := pm.Teams[teamKey]; ok {
			targetWon = teamInfo.HasWon
			roundsWon = teamInfo.RoundsWon
			roundsLost = teamInfo.RoundsLost

			if targetWon {
				resultStr = "승리"
				totalWins++
			} else if roundsWon == roundsLost && roundsWon > 0 {
				isDraw = true
				resultStr = "무승부"
				totalDraws++
			} else {
				resultStr = "패배"
				totalLosses++
			}
		}

		rounds := pm.RoundsPlayed
		if rounds <= 0 {
			rounds = roundsWon + roundsLost
		}
		targetACS := calcACS(targetPlayer.Score, rounds)
		totalKills += targetPlayer.Kills
		totalDeaths += targetPlayer.Deaths
		totalAssists += targetPlayer.Assists
		if rounds > 0 {
			totalACS += targetACS
			acsSamples++
		}

		participants := make([]MatchParticipant, 0, len(pm.Players))
		for _, p := range pm.Players {
			isTarget := strings.EqualFold(p.PUUID, targetPUUID)
			isAlly := !isTarget && p.Team != "" && strings.EqualFold(p.Team, targetPlayer.Team)
			sameParty := !isTarget && targetPlayer.PartyID != "" && p.PartyID == targetPlayer.PartyID
			pACS := calcACS(p.Score, rounds)
			fullName := formatRiotID(p.Name, p.Tag)

			participants = append(participants, MatchParticipant{
				PUUID:     p.PUUID,
				Name:      p.Name,
				Tag:       p.Tag,
				FullName:  fullName,
				Team:      p.Team,
				IsTarget:  isTarget,
				IsAlly:    isAlly,
				SameParty: sameParty,
				PartyID:   p.PartyID,
				Agent:     p.Agent,
				AgentID:   p.AgentID,
				Tier:      p.Tier,
				TierName:  p.TierName,
				Kills:     p.Kills,
				Deaths:    p.Deaths,
				Assists:   p.Assists,
				Score:     p.Score,
				ACS:       round1(pACS),
				Damage:    p.Damage,
			})

			if isTarget || IsExcludedFromAbusing(pm.ModeID, pm.Mode) {
				continue
			}

			ps, exists := statsMap[p.PUUID]
			if !exists {
				ps = &PlayerStat{
					PUUID:      p.PUUID,
					Name:       fullName,
					GameName:   p.Name,
					TagLine:    p.Tag,
					LatestTier: p.TierName,
				}
				statsMap[p.PUUID] = ps
			} else if ps.Name == "비공개 유저" && fullName != "비공개 유저" {
				ps.Name = fullName
				ps.GameName = p.Name
				ps.TagLine = p.Tag
			}

			ps.Met++
			if sameParty {
				ps.SamePartyCount++
			} else {
				ps.SoloMet++
			}
			ps.Kills += float64(p.Kills)
			ps.Deaths += float64(p.Deaths)
			ps.Assists += float64(p.Assists)
			ps.Score += float64(p.Score)
			ps.AvgACS += pACS

			relation := "적군"
			if isAlly {
				relation = "아군"
				ps.AsAlly++
				if !sameParty {
					ps.SoloAsAlly++
				}
				ps.ACSAsAlly += pACS
				if targetWon {
					ps.AllyWon++
				} else if isDraw {
					ps.AllyDraw++
				} else {
					ps.AllyLost++
					if !sameParty {
						ps.SoloAllyLost++
					}
					ps.TargetLost++
				}
			} else {
				ps.AsEnemy++
				ps.ACSAsEnemy += pACS
				if targetWon {
					ps.EnemyWon++
				} else if isDraw {
					ps.EnemyDraw++
				} else {
					ps.EnemyLost++
					ps.TargetLost++
				}
			}

			ps.Encounters = append(ps.Encounters, PlayerEncounter{
				MatchID:      pm.MatchID,
				Map:          pm.Map,
				Mode:         pm.Mode,
				GameStart:    pm.GameStart,
				RoundsWon:    roundsWon,
				RoundsLost:   roundsLost,
				TargetResult: resultStr,
				Relation:     relation,
				SameParty:    sameParty,
				TheirAgent:   p.Agent,
				TheirAgentID: p.AgentID,
				TheirTier:    p.TierName,
				TheirKills:   p.Kills,
				TheirDeaths:  p.Deaths,
				TheirAssists: p.Assists,
				TheirACS:     round1(pACS),
				TargetAgent:  targetPlayer.Agent,
				TargetKDA:    fmt.Sprintf("%d/%d/%d", targetPlayer.Kills, targetPlayer.Deaths, targetPlayer.Assists),
			})
		}

		// Order participants: target's team first (sorted by score desc), then enemy team (sorted by score desc).
		sort.SliceStable(participants, func(i, j int) bool {
			iFriendly := participants[i].IsTarget || participants[i].IsAlly
			jFriendly := participants[j].IsTarget || participants[j].IsAlly
			if iFriendly != jFriendly {
				return iFriendly
			}
			return participants[i].Score > participants[j].Score
		})

		histories = append(histories, MatchSummary{
			MatchID:      pm.MatchID,
			Map:          pm.Map,
			Mode:         pm.Mode,
			ModeID:       pm.ModeID,
			GameStart:    pm.GameStart,
			GameLength:   pm.GameLength,
			RoundsPlayed: rounds,
			RoundsWon:    roundsWon,
			RoundsLost:   roundsLost,
			Agent:        targetPlayer.Agent,
			AgentID:      targetPlayer.AgentID,
			Tier:         targetPlayer.Tier,
			TierName:     targetPlayer.TierName,
			Result:       resultStr,
			Kills:        targetPlayer.Kills,
			Deaths:       targetPlayer.Deaths,
			Assists:      targetPlayer.Assists,
			Score:        targetPlayer.Score,
			ACS:          round1(targetACS),
			Participants: participants,
		})
	}

	// Finalize player stats, compute suspicion score, and collect findings.
	players := make([]PlayerStat, 0, len(statsMap))
	var findings []string
	var repeatCount, suspiciousCount int

	for _, ps := range statsMap {
		if ps.Met > 0 {
			ps.AvgACS = round1(ps.AvgACS / float64(ps.Met))
		}
		if ps.AsAlly > 0 {
			ps.ACSAsAlly = round1(ps.ACSAsAlly / float64(ps.AsAlly))
		}
		if ps.AsEnemy > 0 {
			ps.ACSAsEnemy = round1(ps.ACSAsEnemy / float64(ps.AsEnemy))
		}

		ps.QuickRequeueCount = countQuickRequeues(ps.Encounters)
		evaluateSuspicion(ps)

		if ps.SoloMet >= 2 {
			repeatCount++
		}
		if ps.SuspicionLevel == "high" {
			suspiciousCount++
			findings = append(findings, fmt.Sprintf("[%s] %s", ps.Name, strings.Join(ps.SuspicionReasons, " · ")))
		}
		players = append(players, *ps)
	}

	// Populate metCount & suspect flags back onto each match's participants for instant Tracker-style highlights.
	for i := range histories {
		repeatInMatch := 0
		suspectInMatch := 0
		for j := range histories[i].Participants {
			p := &histories[i].Participants[j]
			if p.IsTarget {
				continue
			}
			if ps, ok := statsMap[p.PUUID]; ok {
				p.MetCount = ps.Met
				if ps.Met >= 2 && !p.SameParty {
					repeatInMatch++
				}
				if ps.SuspicionLevel == "high" {
					suspectInMatch++
				}
			} else {
				p.MetCount = 1
			}
		}
		histories[i].RepeatCount = repeatInMatch
		histories[i].SuspectCount = suspectInMatch
	}

	sort.Slice(players, func(i, j int) bool {
		if players[i].SuspicionScore != players[j].SuspicionScore {
			return players[i].SuspicionScore > players[j].SuspicionScore
		}
		if players[i].SoloMet != players[j].SoloMet {
			return players[i].SoloMet > players[j].SoloMet
		}
		if players[i].Met != players[j].Met {
			return players[i].Met > players[j].Met
		}
		return players[i].Name < players[j].Name
	})

	totalMatches := len(histories)
	var avgK, avgD, avgA, kda, winRate, avgACS float64
	if totalMatches > 0 {
		avgK = round1(float64(totalKills) / float64(totalMatches))
		avgD = round1(float64(totalDeaths) / float64(totalMatches))
		avgA = round1(float64(totalAssists) / float64(totalMatches))
		if totalDeaths == 0 {
			kda = float64(totalKills + totalAssists)
		} else {
			kda = math.Round((float64(totalKills+totalAssists)/float64(totalDeaths))*100) / 100
		}
		decided := totalWins + totalLosses
		if decided > 0 {
			winRate = round1(float64(totalWins) / float64(decided) * 100)
		}
		if acsSamples > 0 {
			avgACS = round1(totalACS / float64(acsSamples))
		}
	}

	return AnalysisResult{
		MatchesCount:    totalMatches,
		ModeFilter:      modeFilter,
		AbusingDetected: len(findings) > 0,
		Summary: OverallSummary{
			TotalMatches:       totalMatches,
			Wins:               totalWins,
			Losses:             totalLosses,
			Draws:              totalDraws,
			WinRate:            winRate,
			AvgKills:           avgK,
			AvgDeaths:          avgD,
			AvgAssists:         avgA,
			KDA:                kda,
			AvgACS:             avgACS,
			RepeatPlayersCount: repeatCount,
			SuspiciousCount:    suspiciousCount,
		},
		Details: findings,
		Players: players,
		History: histories,
	}
}

func countQuickRequeues(encounters []PlayerEncounter) int {
	if len(encounters) < 2 {
		return 0
	}
	quick := 0
	// Encounters are newest-first. Check gaps <= 55 minutes between match starts (roughly immediate requeue).
	for i := 0; i < len(encounters)-1; i++ {
		if encounters[i].GameStart > 0 && encounters[i+1].GameStart > 0 {
			diff := encounters[i].GameStart - encounters[i+1].GameStart
			if diff > 0 && diff <= 55*60 {
				quick++
			}
		}
	}
	return quick
}

func evaluateSuspicion(ps *PlayerStat) {
	// Party friends or players never faced as an enemy are not flagged.
	if ps.SoloMet == 0 || ps.AsEnemy == 0 {
		ps.SuspicionLevel = "none"
		ps.SuspicionScore = 0
		return
	}

	score := 0
	var reasons []string
	decidedEnemy := ps.EnemyWon + ps.EnemyLost

	// 1. Extreme enemy outcome pattern (either repeated losses OR repeated wins against same solo enemy)
	if decidedEnemy >= 3 {
		lossRate := float64(ps.EnemyLost) / float64(decidedEnemy)
		winRate := float64(ps.EnemyWon) / float64(decidedEnemy)

		if lossRate >= 0.75 {
			score += 55
			reasons = append(reasons, fmt.Sprintf("적군 %d전 중 %d패 (패배율 %.0f%%)", ps.AsEnemy, ps.EnemyLost, lossRate*100))
		} else if lossRate >= 0.60 {
			score += 30
			reasons = append(reasons, fmt.Sprintf("적군 %d전 중 %d패 (패배율 %.0f%%)", ps.AsEnemy, ps.EnemyLost, lossRate*100))
		} else if winRate >= 0.75 {
			score += 55
			reasons = append(reasons, fmt.Sprintf("적군 %d전 중 %d승 (승률 %.0f%% · 밀어주기 의심)", ps.AsEnemy, ps.EnemyWon, winRate*100))
		} else if winRate >= 0.60 {
			score += 30
			reasons = append(reasons, fmt.Sprintf("적군 %d전 중 %d승 (승률 %.0f%%)", ps.AsEnemy, ps.EnemyWon, winRate*100))
		}
	} else if ps.AsEnemy == 2 && ps.QuickRequeueCount > 0 {
		if ps.EnemyLost == 2 {
			score += 35
			reasons = append(reasons, "적군 연속 매칭 2전 2패")
		} else if ps.EnemyWon == 2 {
			score += 35
			reasons = append(reasons, "적군 연속 매칭 2전 2승 (밀어주기 의심)")
		}
	}

	// 2. Win-trade ACS discrepancy (large combat score swing between ally vs enemy)
	if ps.SoloAsAlly >= 1 && ps.ACSAsEnemy > 0 && ps.ACSAsAlly > 0 {
		if ps.ACSAsEnemy-ps.ACSAsAlly >= 75 && (ps.SoloAllyLost > 0 || ps.EnemyLost > 0) {
			score += 30
			reasons = append(reasons, fmt.Sprintf("아군/적군 ACS 편차 큼 (아군 %.0f vs 적군 %.0f)", ps.ACSAsAlly, ps.ACSAsEnemy))
		} else if ps.ACSAsAlly-ps.ACSAsEnemy >= 75 && ps.EnemyWon > 0 {
			score += 30
			reasons = append(reasons, fmt.Sprintf("적군일 때 ACS 급감 (아군 %.0f vs 적군 %.0f)", ps.ACSAsAlly, ps.ACSAsEnemy))
		}
	}

	// 3. Quick requeue / sniping bonus (only when an outcome/ACS signal exists or 2+ back-to-back requeues)
	if ps.QuickRequeueCount >= 2 {
		score += 25
		reasons = append(reasons, fmt.Sprintf("연속 동시 큐 추정 %d회", ps.QuickRequeueCount))
	} else if ps.QuickRequeueCount == 1 && score > 0 {
		score += 10
		reasons = append(reasons, "단시간 재매칭 1회")
	}

	if score > 100 {
		score = 100
	}
	ps.SuspicionScore = score
	ps.SuspicionReasons = reasons

	switch {
	case score >= 55:
		ps.SuspicionLevel = "high"
	case score >= 30:
		ps.SuspicionLevel = "watch"
	default:
		ps.SuspicionLevel = "none"
	}
}

func calcACS(score, rounds int) float64 {
	if rounds <= 0 || score <= 0 {
		return 0
	}
	return float64(score) / float64(rounds)
}

func round1(v float64) float64 {
	return math.Round(v*10) / 10
}

func formatRiotID(name, tag string) string {
	if name == "" && tag == "" {
		return "비공개 유저"
	}
	if tag == "" {
		return name
	}
	return name + "#" + tag
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func asMap(v interface{}) (map[string]interface{}, bool) {
	if v == nil {
		return nil, false
	}
	switch m := v.(type) {
	case map[string]interface{}:
		return m, true
	case primitive.M:
		return map[string]interface{}(m), true
	case primitive.D:
		out := make(map[string]interface{}, len(m))
		for _, elem := range m {
			out[elem.Key] = elem.Value
		}
		return out, true
	default:
		return nil, false
	}
}

func asSlice(v interface{}) ([]interface{}, bool) {
	if v == nil {
		return nil, false
	}
	switch s := v.(type) {
	case []interface{}:
		return s, true
	case primitive.A:
		return []interface{}(s), true
	default:
		return nil, false
	}
}

func asString(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func asBool(v interface{}) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	return false
}

func asInt(v interface{}) int {
	return int(asInt64(v))
}

func asInt64(v interface{}) int64 {
	switch n := v.(type) {
	case int:
		return int64(n)
	case int32:
		return int64(n)
	case int64:
		return n
	case float64:
		return int64(n)
	case float32:
		return int64(n)
	default:
		return 0
	}
}
