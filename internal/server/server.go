package server

import (
	"cs2-stat/internal/database"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	_ "github.com/joho/godotenv/autoload"
	_ "github.com/mattn/go-sqlite3"
)

type Server struct {
	port         int
	db           *database.Queries
	dbConn       *sql.DB
	faceitApiKey string
}

func NewServer() *http.Server {
	port, _ := strconv.Atoi(os.Getenv("PORT"))
	dbUrl := os.Getenv("DATABASE_URL")
	if dbUrl == "" {
		log.Fatal("DATABASE_URL MUST BE SET")
	}
	faceitApiKey := os.Getenv("FACEIT_API_KEY")
	if faceitApiKey == "" {
		log.Fatal("FACEIT_API_KEY MUST BE SET")
	}

	dbConn, err := sql.Open("sqlite3", dbUrl)
	if err != nil {
		log.Fatalf("fatal: %s", err)
	}
	db := database.New(dbConn)

	NewServer := &Server{
		port:         port,
		db:           db,
		dbConn:       dbConn,
		faceitApiKey: faceitApiKey,
	}
	log.Print("connected to db")

	go NewServer.StartFetchAndScrape()

	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", NewServer.port),
		Handler:      NewServer.RegisterRoutes(),
		IdleTimeout:  time.Minute,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
	}
	return server
}

func (s *Server) StartFetchAndScrape() {
	err := s.FetchAndScrape()
	if err != nil {
		log.Printf("error: %s", err)
	}
}
