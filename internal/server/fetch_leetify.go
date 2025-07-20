package server

import (
	"context"
	"log"
	"strconv"
	"sync"
	"time"

	"github.com/chromedp/chromedp"
)

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

const leetifyUserURL string = "https://leetify.com/app/profile/"

// ScrapedMatchData represents the raw data scraped from a match page
type ScrapedMatchData struct {
	Data [][]string
	URL  string
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
		if matchResult == "TIE" {
			log.Println("Tie detected, skipping...")
			continue
		}
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
