// Code generated by sqlc. DO NOT EDIT.
// versions:
//   sqlc v1.29.0

package database

import (
	"time"
)

type Player struct {
	SteamID   interface{}
	Name      string
	Country   string
	FaceitUrl string
	Avatar    string
	CreatedAt time.Time
	UpdatedAt time.Time
}
