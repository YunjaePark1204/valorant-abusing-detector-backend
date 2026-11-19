package detector

import (
	"testing"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

func makeV3Match(id, mode, mapName string, gameStart int64, blueWon bool, blueRounds, redRounds int, players []map[string]interface{}) map[string]interface{} {
	list := make([]interface{}, len(players))
	for i, p := range players {
		list[i] = p
	}
	return map[string]interface{}{
		"metadata": map[string]interface{}{
			"matchid":     id,
			"mode":        mode,
			"map":         mapName,
			"game_start":  float64(gameStart),
			"game_length": float64(1800),
		},
		"teams": map[string]interface{}{
			"blue": map[string]interface{}{
				"has_won":     blueWon,
				"rounds_won":  float64(blueRounds),
				"rounds_lost": float64(redRounds),
			},
			"red": map[string]interface{}{
				"has_won":     !blueWon && blueRounds != redRounds,
				"rounds_won":  float64(redRounds),
				"rounds_lost": float64(blueRounds),
			},
		},
		"players": map[string]interface{}{
			"all_players": list,
		},
	}
}

func makeV4Match(id, modeID, modeName, mapName, startedAt string, blueWon bool, blueRounds, redRounds int, players []interface{}) map[string]interface{} {
	return primitive.M{
		"metadata": primitive.M{
			"match_id":           id,
			"map":                primitive.M{"name": mapName},
			"started_at":         startedAt,
			"game_length_in_ms":  int64(1900000),
			"queue":              primitive.M{"id": modeID, "name": modeName},
		},
		"teams": primitive.A{
			primitive.M{"team_id": "Blue", "won": blueWon, "rounds": primitive.M{"won": blueRounds, "lost": redRounds}},
			primitive.M{"team_id": "Red", "won": !blueWon && blueRounds != redRounds, "rounds": primitive.M{"won": redRounds, "lost": blueRounds}},
		},
		"players": primitive.A(players),
	}
}

func v3Player(puuid, name, tag, team, party, agent string, tier int, tierName string, k, d, a, score int) map[string]interface{} {
	return map[string]interface{}{
		"puuid":               puuid,
		"name":                name,
		"tag":                 tag,
		"team":                team,
		"party_id":            party,
		"character":           agent,
		"currenttier":         float64(tier),
		"currenttier_patched": tierName,
		"stats": map[string]interface{}{
			"kills":   float64(k),
			"deaths":  float64(d),
			"assists": float64(a),
			"score":   float64(score),
		},
	}
}

func v4Player(puuid, name, tag, team, party, agent, agentID string, tier int, tierName string, k, d, a, score int) primitive.M {
	return primitive.M{
		"puuid":    puuid,
		"name":     name,
		"tag":      tag,
		"team_id":  team,
		"party_id": party,
		"agent":    primitive.M{"id": agentID, "name": agent},
		"tier":     primitive.M{"id": tier, "name": tierName},
		"stats": primitive.M{
			"kills":   k,
			"deaths":  d,
			"assists": a,
			"score":   score,
		},
	}
}

func TestAnalyzeMatches_ExcludesDeathmatchAndDetectsEnemyAbuser(t *testing.T) {
	target := "target-puuid"
	abuser := "abuser-puuid"
	friend := "friend-puuid"
	dmPlayer := "dm-puuid"

	matches := []map[string]interface{}{
		// 3 competitive losses against abuser (quick requeues), with party friend
		makeV3Match("m1", "Competitive", "Ascent", 1700000000, false, 9, 13, []map[string]interface{}{
			v3Player(target, "Target", "KR1", "Blue", "p-target", "Jett", 20, "Ascendant 2", 18, 16, 4, 4400),
			v3Player(friend, "Friend", "KR1", "Blue", "p-target", "Sova", 20, "Ascendant 2", 15, 15, 8, 3900),
			v3Player(abuser, "Sniper", "KR1", "Red", "p-enemy", "Reyna", 21, "Ascendant 3", 28, 12, 3, 6600),
		}),
		makeV3Match("m2", "Competitive", "Bind", 1700002400, false, 7, 13, []map[string]interface{}{
			v3Player(target, "Target", "KR1", "Blue", "p-target2", "Jett", 20, "Ascendant 2", 14, 17, 2, 3500),
			v3Player(friend, "Friend", "KR1", "Blue", "p-target2", "Omen", 20, "Ascendant 2", 12, 16, 7, 3200),
			v3Player(abuser, "Sniper", "KR1", "Red", "p-enemy2", "Reyna", 21, "Ascendant 3", 25, 11, 5, 6000),
		}),
		// 3rd match in v4 + BSON primitive format
		makeV4Match("m3", "competitive", "Competitive", "Haven", "2023-11-14T23:20:00Z", false, 10, 13, []interface{}{
			v4Player(target, "Target", "KR1", "Blue", "p-target3", "Jett", "agent-jett", 20, "Ascendant 2", 19, 18, 3, 4600),
			v4Player(friend, "Friend", "KR1", "Blue", "p-target3", "Killjoy", "agent-kj", 20, "Ascendant 2", 16, 15, 5, 4000),
			v4Player(abuser, "Sniper", "KR1", "Red", "p-enemy3", "Reyna", "agent-reyna", 21, "Ascendant 3", 30, 14, 4, 7000),
		}),
		// 3 Deathmatches against dmPlayer (should be completely ignored in competitive mode!)
		makeV3Match("dm1", "Deathmatch", "Ascent", 1700010000, false, 0, 0, []map[string]interface{}{
			v3Player(target, "Target", "KR1", "", "", "Jett", 0, "Unranked", 20, 20, 2, 2000),
			v3Player(dmPlayer, "DMGuy", "KR1", "", "", "Reyna", 0, "Unranked", 40, 15, 1, 4000),
		}),
		makeV3Match("dm2", "Deathmatch", "Bind", 1700011000, false, 0, 0, []map[string]interface{}{
			v3Player(target, "Target", "KR1", "", "", "Jett", 0, "Unranked", 20, 20, 2, 2000),
			v3Player(dmPlayer, "DMGuy", "KR1", "", "", "Reyna", 0, "Unranked", 40, 15, 1, 4000),
		}),
		makeV3Match("dm3", "Deathmatch", "Haven", 1700012000, false, 0, 0, []map[string]interface{}{
			v3Player(target, "Target", "KR1", "", "", "Jett", 0, "Unranked", 20, 20, 2, 2000),
			v3Player(dmPlayer, "DMGuy", "KR1", "", "", "Reyna", 0, "Unranked", 40, 15, 1, 4000),
		}),
	}

	res := AnalyzeMatches(matches, target, "competitive")

	if res.MatchesCount != 3 {
		t.Fatalf("expected 3 competitive matches, got %d", res.MatchesCount)
	}
	if !res.AbusingDetected {
		t.Fatalf("expected abusingDetected=true")
	}
	if len(res.Players) != 2 {
		t.Fatalf("expected 2 non-target competitive players, got %d", len(res.Players))
	}

	// First sorted player should be the abuser
	top := res.Players[0]
	if top.PUUID != abuser {
		t.Fatalf("expected abuser first, got %s", top.PUUID)
	}
	if top.SuspicionLevel != "high" {
		t.Fatalf("expected abuser suspicionLevel=high, got %s (score=%d)", top.SuspicionLevel, top.SuspicionScore)
	}
	if top.AsEnemy != 3 || top.EnemyLost != 3 {
		t.Fatalf("expected 3 enemy games & 3 losses, got %d/%d", top.AsEnemy, top.EnemyLost)
	}
	if len(top.Encounters) != 3 {
		t.Fatalf("expected 3 encounters, got %d", len(top.Encounters))
	}

	// Party friend should NOT be marked suspicious
	second := res.Players[1]
	if second.PUUID != friend {
		t.Fatalf("expected friend second, got %s", second.PUUID)
	}
	if second.SamePartyCount != 3 || second.SuspicionLevel != "none" {
		t.Fatalf("expected friend samePartyCount=3 & suspicion=none, got %d / %s", second.SamePartyCount, second.SuspicionLevel)
	}

	// Even when modeFilter is "all", Deathmatch should not flag dmPlayer as abusing
	resAll := AnalyzeMatches(matches, target, "all")
	if resAll.MatchesCount != 6 {
		t.Fatalf("expected 6 matches in all mode, got %d", resAll.MatchesCount)
	}
	for _, p := range resAll.Players {
		if p.PUUID == dmPlayer {
			t.Fatalf("dmPlayer should be excluded from abusing stats even in 'all' mode")
		}
	}
}

func TestAnalyzeMatches_IgnoresAllyOnlyLossesAndDetectsWinTrader(t *testing.T) {
	target := "target-puuid"
	trader := "trader-puuid"
	normalAlly := "normal-ally-puuid"

	matches := []map[string]interface{}{
		// Trader is on target's team (non-party) and throws hard (low ACS) -> loss
		makeV3Match("wt1", "Competitive", "Ascent", 1700020000, false, 5, 13, []map[string]interface{}{
			v3Player(target, "Target", "KR1", "Blue", "p1", "Jett", 20, "Ascendant 2", 20, 14, 3, 4800),
			v3Player(trader, "Trader", "KR1", "Blue", "p2", "Sova", 20, "Ascendant 2", 4, 17, 1, 1440),     // ACS = 80
			v3Player(normalAlly, "Ally", "KR1", "Blue", "p2b", "Omen", 20, "Ascendant 2", 14, 15, 5, 3600), // Normal ally
		}),
		// Trader is on target's team again -> loss
		makeV3Match("wt2", "Competitive", "Lotus", 1700022200, false, 6, 13, []map[string]interface{}{
			v3Player(target, "Target", "KR1", "Blue", "p3", "Jett", 20, "Ascendant 2", 18, 15, 2, 4300),
			v3Player(trader, "Trader", "KR1", "Blue", "p4", "Fade", 20, "Ascendant 2", 5, 18, 2, 1710), // ACS = 90
			v3Player(normalAlly, "Ally", "KR1", "Blue", "p4b", "Omen", 20, "Ascendant 2", 15, 15, 6, 3800),
		}),
		// Trader is on enemy team -> plays great (high ACS) -> target loses
		makeV3Match("wt3", "Competitive", "Haven", 1700024400, false, 8, 13, []map[string]interface{}{
			v3Player(target, "Target", "KR1", "Blue", "p5", "Jett", 20, "Ascendant 2", 17, 16, 4, 4200),
			v3Player(trader, "Trader", "KR1", "Red", "p6", "Reyna", 20, "Ascendant 2", 29, 12, 3, 6300), // ACS = 300
			v3Player(normalAlly, "Ally", "KR1", "Blue", "p5b", "Omen", 20, "Ascendant 2", 16, 16, 4, 4000),
		}),
	}

	res := AnalyzeMatches(matches, target, "competitive")
	if len(res.Players) != 2 {
		t.Fatalf("expected 2 players, got %d", len(res.Players))
	}

	var traderStat, allyStat *PlayerStat
	for i := range res.Players {
		if res.Players[i].PUUID == trader {
			traderStat = &res.Players[i]
		} else if res.Players[i].PUUID == normalAlly {
			allyStat = &res.Players[i]
		}
	}

	if allyStat == nil || allyStat.SuspicionLevel != "none" || allyStat.SuspicionScore != 0 {
		t.Fatalf("expected ally-only player not to be flagged, got %+v", allyStat)
	}
	if traderStat == nil || traderStat.SuspicionLevel != "high" || traderStat.SuspicionScore < 55 {
		t.Fatalf("expected win-trader high suspicion, got %+v", traderStat)
	}
}

func TestAnalyzeMatches_DetectsRepeatedEnemyWinsAsAbusing(t *testing.T) {
	target := "target-puuid"
	feeder := "feeder-puuid"

	matches := []map[string]interface{}{
		makeV3Match("w1", "Competitive", "Ascent", 1700030000, true, 13, 4, []map[string]interface{}{
			v3Player(target, "Target", "KR1", "Blue", "p1", "Jett", 20, "Ascendant 2", 24, 10, 3, 5200),
			v3Player(feeder, "Feeder", "KR1", "Red", "p2", "Reyna", 20, "Ascendant 2", 5, 16, 1, 1360),
		}),
		makeV3Match("w2", "Competitive", "Bind", 1700032000, true, 13, 5, []map[string]interface{}{
			v3Player(target, "Target", "KR1", "Blue", "p3", "Jett", 20, "Ascendant 2", 22, 11, 4, 4900),
			v3Player(feeder, "Feeder", "KR1", "Red", "p4", "Reyna", 20, "Ascendant 2", 6, 17, 2, 1600),
		}),
		makeV3Match("w3", "Competitive", "Haven", 1700034000, true, 13, 6, []map[string]interface{}{
			v3Player(target, "Target", "KR1", "Blue", "p5", "Jett", 20, "Ascendant 2", 25, 12, 2, 5400),
			v3Player(feeder, "Feeder", "KR1", "Red", "p6", "Reyna", 20, "Ascendant 2", 7, 17, 1, 1700),
		}),
	}

	res := AnalyzeMatches(matches, target, "competitive")
	if !res.AbusingDetected || len(res.Players) != 1 {
		t.Fatalf("expected abusingDetected=true for repeated enemy wins, got %+v", res)
	}
	p := res.Players[0]
	if p.EnemyWon != 3 || p.SuspicionLevel != "high" || p.SuspicionScore < 75 {
		t.Fatalf("expected 3 enemy wins and high suspicion, got %+v", p)
	}
}
