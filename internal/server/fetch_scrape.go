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

func (s *Server) FetchAndScrape() error {
	log.Println("Starting fetching and scraping...")

	parentCtx := context.Background()
	ctx, cancel := context.WithTimeout(parentCtx, 5*time.Minute)
	defer cancel()

	client := &http.Client{}

	// fetch top players on faceit leaderboard
	playersEU, err := s.getTopPlayers(ctx, client, "EU", 5)
	if err != nil {
		return fmt.Errorf("error: failed to get top EU players: %s", err)
	}

	log.Println("Player fetch start", playersEU.Start)
	log.Println("Player fetch end", playersEU.End)

	// take resulting player IDs and extract them into a slice
	playerIDs := []string{}
	for _, player := range playersEU.Items {
		playerIDs = append(playerIDs, player.PlayerID)
	}
	log.Println("Players gathered from faceit:", len(playerIDs))

	// get player details (steamID) from faceit
	playerDetails, err := s.getPlayerDetailsWithWorkers(ctx, client, playerIDs)
	if err != nil {
		return fmt.Errorf("error: failed to get player details: %s", err)
	}
	log.Println("Player details gathered from faceit:", len(playerDetails))

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

	// create chromedp context to share between scraping functions
	allocCtx, cancel := chromedp.NewExecAllocator(context.Background(), chromedp.DefaultExecAllocatorOptions[:]...)
	defer cancel()
	scrapeCtx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	log.Println("Starting to scrape user profiles for matches...")
	matchLinks, err := s.scrapeMatchLinksWithWorkers(scrapeCtx, leetifyURLs)
	if err != nil {
		return err
	}
	fmt.Printf("Match links found: %d\n", len(matchLinks))

	log.Println("Starting to scrape matches for stats...")
	matches, err := s.scrapeMatchesWithWorkers(scrapeCtx, matchLinks)
	if err != nil {
		return err
	}
	log.Println("Matches scraped:", len(matches))

	avgMatchStats, err := getAverageMatchStats(matches)
	if err != nil {
		return fmt.Errorf("error calculating average match stats: %s", err)
	}
	log.Println("Matches analyzed:", len(avgMatchStats))

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
	log.Println("Matches inserted into db:", len(avgMatchStats))

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
