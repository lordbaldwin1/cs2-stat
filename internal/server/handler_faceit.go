package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
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

const topPlayersURL string = "https://open.faceit.com/data/v4/rankings/games/cs2/regions/"
const playerDetailsURL string = "https://open.faceit.com/data/v4/players/"

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

	return nil
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
