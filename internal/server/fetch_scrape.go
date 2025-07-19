package server

import (
	"context"
	"cs2-stat/internal/database"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
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
	Teams [2]Team // [0]: winner, [1]: loser
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

	// get player details (steamID) from faceit
	playerDetails, err := s.getPlayerDetailsWithWorkers(ctx, client, playerIDs)
	if err != nil {
		return fmt.Errorf("error: failed to get player details: %s", err)
	}

	for _, player := range playerDetails {
		fmt.Printf("steamID: %s, name: %s, country: %s, faceitURL: %s, avatar: %s\n", player.SteamID64, player.Nickname, player.Country, player.FaceitURL, player.Avatar)
	}

	for _, player := range playerDetails {
		faceitURL := strings.ReplaceAll(player.FaceitURL, "{lang}", "en")
		log.Println("Adding player to db: ", player.Nickname)
		log.Println(faceitURL)
		addedPlayer, err := s.db.CreatePlayer(ctx, database.CreatePlayerParams{
			SteamID:   player.SteamID64,
			Name:      player.Nickname,
			Country:   player.Country,
			FaceitUrl: faceitURL,
			Avatar:    player.Avatar,
		})
		if err != nil {
			return fmt.Errorf("error: %s", err)
		}
		log.Printf("Added player: %s to db\n", addedPlayer.Name)
	}

	var leetifyURLs []string
	for _, playerDetail := range playerDetails {
		url := leetifyUserURL + playerDetail.SteamID64
		leetifyURLs = append(leetifyURLs, url)
	}

	allocCtx, cancel := chromedp.NewExecAllocator(context.Background(), chromedp.DefaultExecAllocatorOptions[:]...)
	defer cancel()
	scrapeCtx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	// concurrent scraping of match URLs
	// NOTE: Fix error handling, currently returns no errors
	log.Println("Starting to scrape user profiles for matches...")
	matchLinks, err := s.scrapeMatchLinksWithWorkers(scrapeCtx, leetifyURLs)
	if err != nil {
		return err
	}
	fmt.Println("Matches found:", len(matchLinks))

	// concurrent scraping of match tables from URLs
	// NOTE: Fix error handling, currently returns no errors
	log.Println("Starting to scrape matches for stats...")
	matches, err := s.scrapeMatchesWithWorkers(scrapeCtx, matchLinks)
	if err != nil {
		return err
	}

	for _, match := range matches {
		fmt.Println()
		fmt.Println("Matches: ")
		fmt.Println("Winning team:")
		fmt.Println(match.Teams[0])
		fmt.Println()
		fmt.Println("Losing team:")
		fmt.Println(match.Teams[1])
		fmt.Println()

	}

	return nil
}

func (s *Server) scrapeMatchesWithWorkers(parentCtx context.Context, matchLinks []string) ([]Match, error) {
	numWorkers := 5
	jobs := make(chan string, len(matchLinks))
	results := make(chan [][]string, len(matchLinks)*10)

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

	var allMatches [][][]string
	for matchRows := range results {
		allMatches = append(allMatches, matchRows)
	}

	var matches []Match
	for _, matchRow := range allMatches {
		var nonEmptyRows [][]string
		for _, row := range matchRow {
			if len(row) == 0 {
				continue
			}
			nonEmptyRows = append(nonEmptyRows, row)
		}

		if len(nonEmptyRows) < 10 {
			continue
		}

		var winPlayers, losePlayers []PlayerStats
		for i, row := range nonEmptyRows[:10] {
			p := PlayerStats{
				Name:                row[0],
				LeetifyRating:       row[1],
				PersonalPerformance: row[2],
				HLTVRating:          row[3],
				KD:                  row[4],
				ADR:                 row[5],
				Aim:                 row[6],
				Utility:             row[7],
			}
			if i < 5 {
				winPlayers = append(winPlayers, p)
			} else {
				losePlayers = append(losePlayers, p)
			}
		}
		if len(winPlayers) < 5 || len(losePlayers) < 5 {
			log.Println("Team had less than 5 players")
			continue
		}
		winTeam := Team{
			Players: winPlayers,
			Won:     true,
		}
		loseTeam := Team{
			Players: losePlayers,
			Won:     false,
		}
		match := Match{
			Teams: [2]Team{winTeam, loseTeam},
		}
		matches = append(matches, match)
	}
	return matches, nil
}

func matchesWorker(ctx context.Context, jobs <-chan string, results chan<- [][]string) {
	for matchLink := range jobs {
		tabCtx, cancel := chromedp.NewContext(ctx)
		var matchData [][]string
		err := chromedp.Run(tabCtx,
			chromedp.Navigate(matchLink),
			chromedp.WaitVisible(`table`, chromedp.ByQuery),
			chromedp.Evaluate(`
				Array.from(document.querySelectorAll('table tr'))
					.map(row => Array.from(row.querySelectorAll('td'))
					.map(cell => cell.textContent.trim())
			)
			`, &matchData),
		)
		cancel()
		if err != nil {
			log.Println("Error: ", err)
			continue
		}
		results <- matchData
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

	return matchLinks, nil
}

func matchLinkWorker(ctx context.Context, jobs <-chan string, results chan<- []string) {
	for matchURL := range jobs {
		tabCtx, cancel := chromedp.NewContext(ctx)

		var links []string
		err := chromedp.Run(tabCtx,
			chromedp.Navigate(matchURL),
			chromedp.Sleep(5*time.Second),
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
