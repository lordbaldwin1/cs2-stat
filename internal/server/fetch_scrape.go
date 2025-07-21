package server

import (
	"context"
	"cs2-stat/internal/database"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
)

func (s *Server) FetchAndScrapeJob() error {
	leaderboardStart := 0
	leaderboardEnd := 2000
	offset := 50
	fetchLimit := 50

	log.Println("Starting fetching and scraping...")
	log.Println()

	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(),
		append(chromedp.DefaultExecAllocatorOptions[:],
			chromedp.Flag("headless", true),
			chromedp.Flag("disable-gpu", true),
			chromedp.Flag("no-sandbox", true),
			chromedp.Flag("disable-dev-shm-usage", true),
			chromedp.Flag("disable-web-security", true),
			chromedp.Flag("disable-features", "VizDisplayCompositor"),
			chromedp.Flag("disable-background-timer-throttling", true),
			chromedp.Flag("disable-backgrounding-occluded-windows", true),
			chromedp.Flag("disable-renderer-backgrounding", true),
			chromedp.Flag("disable-ipc-flooding-protection", true),
		)...,
	)
	defer allocCancel()

	scrapeCtx, scrapeCancel := chromedp.NewContext(
		allocCtx,
		chromedp.WithLogf(log.Printf),
	)
	defer scrapeCancel()

	heartbeatCtx, heartbeatCancel := context.WithCancel(scrapeCtx)
	defer heartbeatCancel()

	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-ticker.C:
				var version string
				if err := chromedp.Run(heartbeatCtx, chromedp.Evaluate(`navigator.userAgent`, &version)); err != nil {
					log.Printf("Heartbeat failed: %v", err)
				}
			}
		}
	}()

	for startPos := leaderboardStart; startPos < leaderboardEnd; startPos += offset {
		log.Printf("Scraping leaderboard position: %d to %d...", startPos+1, startPos+offset)

		if startPos > leaderboardStart {
			time.Sleep(2 * time.Second)
		}

		err := s.FetchAndScrape(startPos, fetchLimit, scrapeCtx)
		if err != nil {
			log.Printf("Error in iteration %d-%d: %v", startPos+1, startPos+offset, err)
			continue
		}

		log.Printf("Successfully completed iteration %d-%d", startPos+1, startPos+offset)
	}

	log.Println("Fetching and scraping finished.")
	return nil
}

func (s *Server) FetchAndScrape(startPos int, faceitLimit int, scrapeCtx context.Context) error {
	parentCtx := context.Background()
	ctx, cancel := context.WithTimeout(parentCtx, 5*time.Minute)
	defer cancel()

	client := &http.Client{}

	// fetch top players on faceit leaderboard
	playersEU, err := s.getTopPlayers(ctx, client, "EU", faceitLimit, startPos)
	if err != nil {
		return fmt.Errorf("error: failed to get top EU players: %s", err)
	}

	// take resulting player IDs and extract them into a slice
	playerIDs := []string{}
	for _, player := range playersEU.Items {
		playerIDs = append(playerIDs, player.PlayerID)
	}

	// get player details (steamID) from faceit
	playerDetails, err := s.getPlayerDetailsWithWorkers(ctx, client, playerIDs)
	if err != nil {
		return fmt.Errorf("error: failed to get player details: %s", err)
	}

	for _, player := range playerDetails {
		faceitURL := strings.ReplaceAll(player.FaceitURL, "{lang}", "en")
		_, err := s.db.CreatePlayer(ctx, database.CreatePlayerParams{
			SteamID:   player.SteamID64,
			Name:      player.Nickname,
			Country:   player.Country,
			FaceitUrl: faceitURL,
			Avatar:    player.Avatar,
		})
		if err != nil {
			return fmt.Errorf("error: %s", err)
		}
	}

	var leetifyURLs []string
	for _, playerDetail := range playerDetails {
		url := leetifyUserURL + playerDetail.SteamID64
		leetifyURLs = append(leetifyURLs, url)
	}

	log.Println("Scraping user profiles for matches...")
	matchLinks, err := s.scrapeMatchLinksWithWorkers(scrapeCtx, leetifyURLs)
	if err != nil {
		return err
	}

	log.Println("Scraping matches for stats...")
	matches, err := s.scrapeMatchesWithWorkers(scrapeCtx, matchLinks)
	if err != nil {
		return err
	}

	avgMatchStats, err := getAverageMatchStats(matches)
	if err != nil {
		return fmt.Errorf("error calculating average match stats: %s", err)
	}

	var matchesToInsert []database.CreateMatchParams
	for _, match := range avgMatchStats {
		matchesToInsert = append(matchesToInsert, database.CreateMatchParams{
			MatchUrl:                match.MatchURL,
			WAvgLeetifyRating:       match.WinAvgLeetifyRating,
			WAvgPersonalPerformance: match.WinAvgPersonalPerformance,
			WAvgHltvRating:          match.WinAvgHTLVRating,
			WAvgKd:                  match.WinAvgKD,
			WAvgAim:                 match.WinAvgAim,
			WAvgUtility:             match.WinAvgUtility,
			LAvgLeetifyRating:       match.LossAvgLeetifyRating,
			LAvgPersonalPerformance: match.LossAvgPersonalPerformance,
			LAvgHltvRating:          match.LossAvgHTLVRating,
			LAvgKd:                  match.LossAvgKD,
			LAvgAim:                 match.LossAvgAim,
			LAvgUtility:             match.LossAvgUtility,
		})
	}
	if err = BatchInsertMatches(context.Background(), s.dbConn, matchesToInsert); err != nil {
		return fmt.Errorf("error: failed to batch insert: %w", err)
	}
	log.Println("Matches analyzed and saved:", len(avgMatchStats))

	return nil
}

func BatchInsertMatches(ctx context.Context, db *sql.DB, matches []database.CreateMatchParams) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	qtx := database.New(tx)

	for _, match := range matches {
		if err := qtx.CreateMatch(ctx, match); err != nil {
			tx.Rollback()
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

// =============================================================================
// PRINT FUNCTIONS
// =============================================================================

// func printPlayerDetails(players []PlayerDetails) {
// 	fmt.Println("\n=== FETCHED PLAYER DETAILS ===")
// 	for _, player := range players {
// 		fmt.Printf("  Name: %s\n", player.Nickname)
// 		fmt.Printf("  Steam ID: %s\n", player.SteamID64)
// 		fmt.Printf("  Country: %s\n", player.Country)
// 		fmt.Printf("  Faceit URL: %s\n", player.FaceitURL)
// 		fmt.Printf("  Avatar: %s\n", player.Avatar)
// 		fmt.Println("  " + strings.Repeat("-", 30))
// 	}
// 	fmt.Printf("Total players fetched: %d\n", len(players))
// }

// func printDatabaseOperations(players []PlayerDetails) {
// 	fmt.Println("\n=== DATABASE OPERATIONS ===")
// 	for _, player := range players {
// 		faceitURL := strings.ReplaceAll(player.FaceitURL, "{lang}", "en")
// 		fmt.Printf("  Adding player: %s\n", player.Nickname)
// 		fmt.Printf("  Faceit URL: %s\n", faceitURL)
// 	}
// }

// func printAverageMatchStats(stats []MatchAverageStats) {
// 	fmt.Println("\n=== AVERAGE MATCH STATISTICS ===")
// 	for i, stat := range stats {
// 		fmt.Printf("\nMatch %d:\n", i+1)
// 		fmt.Printf("  URL: %s\n", stat.MatchURL)
// 		fmt.Printf("  WINNING TEAM Averages:\n")
// 		fmt.Printf("    Leetify Rating: %.2f\n", stat.WinAvgLeetifyRating)
// 		fmt.Printf("    Personal Performance: %.2f\n", stat.WinAvgPersonalPerformance)
// 		fmt.Printf("    HLTV Rating: %.2f\n", stat.WinAvgHTLVRating)
// 		fmt.Printf("    K/D Ratio: %.2f\n", stat.WinAvgKD)
// 		fmt.Printf("    Aim: %.2f\n", stat.WinAvgAim)
// 		fmt.Printf("    Utility: %.2f\n", stat.WinAvgUtility)

// 		fmt.Printf("  LOSING TEAM Averages:\n")
// 		fmt.Printf("    Leetify Rating: %.2f\n", stat.LossAvgLeetifyRating)
// 		fmt.Printf("    Personal Performance: %.2f\n", stat.LossAvgPersonalPerformance)
// 		fmt.Printf("    HLTV Rating: %.2f\n", stat.LossAvgHTLVRating)
// 		fmt.Printf("    K/D Ratio: %.2f\n", stat.LossAvgKD)
// 		fmt.Printf("    Aim: %.2f\n", stat.LossAvgAim)
// 		fmt.Printf("    Utility: %.2f\n", stat.LossAvgUtility)
// 		fmt.Println("  " + strings.Repeat("-", 40))
// 	}
// 	fmt.Printf("\nTotal matches analyzed: %d\n", len(stats))
// }
