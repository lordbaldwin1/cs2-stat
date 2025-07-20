package server

import (
	"context"
	"cs2-stat/internal/database"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/chromedp"
)

type Players struct {
	Items []struct {
		PlayerID       string `json:"player_id"`
		Nickname       string `json:"nickname"`
		Country        string `json:"country"`
		Position       int    `json:"position"`
		FaceitElo      int    `json:"faceit_elo"`
		GameSkillLevel int    `json:"game_skill_level"`
	} `json:"items"`
	Start int `json:"start"`
	End   int `json:"end"`
}

type PlayerDetails struct {
	PlayerID  string `json:"player_id"`
	Nickname  string `json:"nickname"`
	Avatar    string `json:"avatar"`
	Country   string `json:"country"`
	SteamID64 string `json:"steam_id_64"`
	FaceitURL string `json:"faceit_url"`
}

type PlayerStats struct {
	Name                string
	LeetifyRating       string
	PersonalPerformance string
	HLTVRating          string
	KD                  string
	ADR                 string
	Aim                 string
	Utility             string
}

type Team struct {
	Players []PlayerStats
	Won     bool
}

type Match struct {
	Teams    [2]Team // [0]: winner, [1]: loser
	MatchURL string
}

type MatchAverageStats struct {
	MatchURL                   string
	WinAvgLeetifyRating        float64
	WinAvgPersonalPerformance  float64
	WinAvgHTLVRating           float64
	WinAvgKD                   float64
	WinAvgAim                  float64
	WinAvgUtility              float64
	LossAvgLeetifyRating       float64
	LossAvgPersonalPerformance float64
	LossAvgHTLVRating          float64
	LossAvgKD                  float64
	LossAvgAim                 float64
	LossAvgUtility             float64
}

// ScrapedMatchData represents the raw data scraped from a match page
type ScrapedMatchData struct {
	Data [][]string
	URL  string
}

const topPlayersURL string = "https://open.faceit.com/data/v4/rankings/games/cs2/regions/"
const playerDetailsURL string = "https://open.faceit.com/data/v4/players/"
const leetifyUserURL string = "https://leetify.com/app/profile/"

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

	return nil
}

func (s *Server) scrapeMatchesWithWorkers(parentCtx context.Context, matchLinks []string) ([]Match, error) {
	numWorkers := 5
	jobs := make(chan string, len(matchLinks))
	results := make(chan ScrapedMatchData, len(matchLinks)*10)

	var wg sync.WaitGroup
	for range numWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			matchesWorker(parentCtx, jobs, results)
		}()
	}

	for _, matchLink := range matchLinks {
		jobs <- matchLink
	}
	close(jobs)

	go func() {
		wg.Wait()
		close(results)
	}()

	// array of all matches with their URLs
	// matches are an array of players with their corresponding URL
	var allMatches []ScrapedMatchData
	for result := range results {
		allMatches = append(allMatches, result)
	}

	var matches []Match
	for _, match := range allMatches {
		// leetify scrape returns some empty arrays
		var validMatches [][]string
		for _, player := range match.Data {
			if len(player) == 0 {
				continue
			}
			validMatches = append(validMatches, player)
		}

		if len(validMatches) < 10 {
			log.Printf("Skipping match %s: only %d valid players (need 10)", match.URL, len(validMatches))
			continue
		}

		var winPlayers, losePlayers []PlayerStats
		for i, player := range validMatches[:10] {
			p := PlayerStats{
				Name:                player[0],
				LeetifyRating:       player[1],
				PersonalPerformance: player[2],
				HLTVRating:          player[3],
				KD:                  player[4],
				ADR:                 player[5],
				Aim:                 player[6],
				Utility:             player[7],
			}
			if i < 5 {
				winPlayers = append(winPlayers, p)
			} else {
				losePlayers = append(losePlayers, p)
			}
		}
		winTeam := Team{
			Players: winPlayers,
			Won:     true,
		}
		loseTeam := Team{
			Players: losePlayers,
			Won:     false,
		}
		matchObj := Match{
			Teams:    [2]Team{winTeam, loseTeam},
			MatchURL: match.URL,
		}
		matches = append(matches, matchObj)
	}

	log.Printf("Successfully processed %d matches out of %d scraped", len(matches), len(allMatches))
	return matches, nil
}

func matchesWorker(ctx context.Context, jobs <-chan string, results chan<- ScrapedMatchData) {
	for matchLink := range jobs {
		tabCtx, cancel := chromedp.NewContext(ctx)
		var matchResult string
		var matchData [][]string
		err := chromedp.Run(tabCtx,
			chromedp.Navigate(matchLink),
			chromedp.WaitVisible(`table`, chromedp.ByQuery),
			chromedp.Sleep(1*time.Second),
			chromedp.Text(`div.phrase`, &matchResult, chromedp.NodeVisible, chromedp.ByQuery),
			chromedp.Evaluate(`
				Array.from(document.querySelectorAll('table tbody tr'))
					.map(row => Array.from(row.querySelectorAll('td'))
					.map(cell => cell.textContent.trim()))
			`, &matchData),
		)
		cancel()
		if err != nil {
			log.Println("Error: ", err)
			continue
		}

		log.Println("Result:", matchResult)
		if matchResult == "TIE" {
			log.Println("Tie detected, skipping...")
			continue
		}

		log.Printf("Scraped %d rows from match %s", len(matchData), matchLink)
		results <- ScrapedMatchData{
			Data: matchData,
			URL:  matchLink,
		}
	}
}

func (s *Server) scrapeMatchLinksWithWorkers(parentCtx context.Context, playerURLs []string) ([]string, error) {
	numWorkers := 5
	jobs := make(chan string, len(playerURLs))
	results := make(chan []string, len(playerURLs))

	var wg sync.WaitGroup
	for range numWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			matchLinkWorker(parentCtx, jobs, results)
		}()
	}

	for _, playerURL := range playerURLs {
		jobs <- playerURL
	}
	close(jobs)

	go func() {
		wg.Wait()
		close(results)
	}()

	var matchLinks []string
	for link := range results {
		matchLinks = append(matchLinks, link...)
	}

	// only append unique matches
	seen := make(map[string]bool)
	var uniqueMatchLinks []string
	for _, link := range matchLinks {
		if !seen[link] {
			seen[link] = true
			uniqueMatchLinks = append(uniqueMatchLinks, link)
		}
	}

	return uniqueMatchLinks, nil
}

func matchLinkWorker(ctx context.Context, jobs <-chan string, results chan<- []string) {
	for matchURL := range jobs {
		tabCtx, cancel := chromedp.NewContext(ctx)

		var links []string
		err := chromedp.Run(tabCtx,
			chromedp.Navigate(matchURL),
			chromedp.WaitVisible(`table`, chromedp.ByQuery),
			chromedp.Evaluate(`
				Array.from(document.querySelectorAll('a.ng-star-inserted[href^="/app/match-details/"]'))
					.slice(0, 5)
					.map(a => a.href)
			`, &links),
		)
		cancel()
		if err != nil {
			log.Println("Error: ", err)
			continue
		}

		results <- links
	}
}

func (s *Server) getTopPlayers(ctx context.Context, client *http.Client, region string, limit int) (*Players, error) {
	url := getTopPlayersURL(region, limit)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "cs2-stat")
	req.Header.Add("Authorization", "Bearer "+s.faceitApiKey)

	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	data, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}

	var players Players
	err = json.Unmarshal(data, &players)
	if err != nil {
		return nil, err
	}

	return &players, nil
}

func (s *Server) getPlayerDetailsWithWorkers(ctx context.Context, client *http.Client, playerIDs []string) ([]PlayerDetails, error) {
	numWorkers := 5
	jobs := make(chan string, len(playerIDs))
	results := make(chan PlayerDetails, len(playerIDs))

	// Start workers
	for range numWorkers {
		go worker(ctx, jobs, results, s, client)
	}

	// Send all jobs
	for _, playerID := range playerIDs {
		jobs <- playerID
	}
	close(jobs)

	// Collect results
	var players []PlayerDetails
	for range playerIDs {
		select {
		case player := <-results:
			players = append(players, player)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	return players, nil
}

func worker(ctx context.Context, jobs <-chan string, results chan<- PlayerDetails, s *Server, client *http.Client) {
	for playerID := range jobs {
		player, err := s.fetchSinglePlayer(ctx, playerID, client)
		if err != nil {
			log.Printf("Error fetching player %s: %v", playerID, err)
			continue
		}
		results <- player
	}
}

func (s *Server) fetchSinglePlayer(ctx context.Context, playerID string, client *http.Client) (PlayerDetails, error) {
	url := getPlayerDetailsURL(playerID)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return PlayerDetails{}, err
	}
	req.Header.Set("User-Agent", "cs2-stat")
	req.Header.Add("Authorization", "Bearer "+s.faceitApiKey)

	res, err := client.Do(req)
	if err != nil {
		return PlayerDetails{}, err
	}
	defer res.Body.Close()

	data, err := io.ReadAll(res.Body)
	if err != nil {
		return PlayerDetails{}, err
	}

	var player PlayerDetails
	err = json.Unmarshal(data, &player)
	if err != nil {
		return PlayerDetails{}, err
	}

	return player, nil
}

func getTopPlayersURL(region string, limit int) string {
	maxLimit := 50
	if limit > maxLimit {
		return fmt.Sprintf("%s%s?offset=0&limit=%d", topPlayersURL, region, maxLimit)
	}
	return fmt.Sprintf("%s%s?offset=0&limit=%d", topPlayersURL, region, limit)
}

func getPlayerDetailsURL(playerID string) string {
	return fmt.Sprintf("%s%s", playerDetailsURL, playerID)
}

func getAverageMatchStats(matches []Match) ([]MatchAverageStats, error) {
	var matchesAverageStats []MatchAverageStats
	const teamSize float64 = 5.0
	for _, match := range matches {
		var (
			winLeetify, winPersonalPerformance, winHLTV, winKD, winAim, winUtility       float64
			lossLeetify, lossPersonalPerformance, lossHLTV, lossKD, lossAim, lossUtility float64
			skipMatch                                                                    bool
		)

		for _, player := range match.Teams[0].Players {
			lr, err := strconv.ParseFloat(player.LeetifyRating, 64)
			if err != nil {
				skipMatch = true
				break
			}
			winLeetify += lr

			pp, err := strconv.ParseFloat(player.PersonalPerformance, 64)
			if err != nil {
				skipMatch = true
				break
			}
			winPersonalPerformance += pp

			hr, err := strconv.ParseFloat(player.HLTVRating, 64)
			if err != nil {
				skipMatch = true
				break
			}
			winHLTV += hr

			kdr, err := strconv.ParseFloat(player.KD, 64)
			if err != nil {
				skipMatch = true
				break
			}
			winKD += kdr

			aim, err := strconv.ParseFloat(player.Aim, 64)
			if err != nil {
				skipMatch = true
				break
			}
			winAim += aim

			util, err := strconv.ParseFloat(player.Utility, 64)
			if err != nil {
				skipMatch = true
				break
			}
			winUtility += util
		}
		if skipMatch {
			continue
		}

		for _, player := range match.Teams[1].Players {
			lr, err := strconv.ParseFloat(player.LeetifyRating, 64)
			if err != nil {
				skipMatch = true
				break
			}
			lossLeetify += lr

			pp, err := strconv.ParseFloat(player.PersonalPerformance, 64)
			if err != nil {
				skipMatch = true
				break
			}
			lossPersonalPerformance += pp

			hr, err := strconv.ParseFloat(player.HLTVRating, 64)
			if err != nil {
				skipMatch = true
				break
			}
			lossHLTV += hr

			kdr, err := strconv.ParseFloat(player.KD, 64)
			if err != nil {
				skipMatch = true
				break
			}
			lossKD += kdr

			aim, err := strconv.ParseFloat(player.Aim, 64)
			if err != nil {
				skipMatch = true
				break
			}
			lossAim += aim

			util, err := strconv.ParseFloat(player.Utility, 64)
			if err != nil {
				skipMatch = true
				break
			}
			lossUtility += util
		}
		if skipMatch {
			log.Println("Skipping match")
			continue
		}
		matchesAverageStats = append(matchesAverageStats, MatchAverageStats{
			MatchURL:                   match.MatchURL,
			WinAvgLeetifyRating:        winLeetify / teamSize,
			WinAvgPersonalPerformance:  winPersonalPerformance / teamSize,
			WinAvgHTLVRating:           winHLTV / teamSize,
			WinAvgKD:                   winKD / teamSize,
			WinAvgAim:                  winAim / teamSize,
			WinAvgUtility:              winUtility / teamSize,
			LossAvgLeetifyRating:       lossLeetify / teamSize,
			LossAvgPersonalPerformance: lossPersonalPerformance / teamSize,
			LossAvgHTLVRating:          lossHLTV / teamSize,
			LossAvgKD:                  lossKD / teamSize,
			LossAvgAim:                 lossAim / teamSize,
			LossAvgUtility:             lossUtility / teamSize,
		})
	}
	return matchesAverageStats, nil
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
