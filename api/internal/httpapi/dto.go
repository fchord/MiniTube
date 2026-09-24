package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"minitube/api/internal/store"
)

type userPublicJSON struct {
	ID        string  `json:"id"`
	Username  string  `json:"username"`
	Nickname  string  `json:"nickname"`
	AvatarURL *string `json:"avatarUrl"`
	Bio       string  `json:"bio"`
	CreatedAt string  `json:"createdAt"`
}

type userPrivateJSON struct {
	userPublicJSON
	Email          string      `json:"email"`
	EmailVerified  bool        `json:"emailVerified"`
	DefaultChannel channelJSON `json:"defaultChannel"`
}

type channelJSON struct {
	ID              string   `json:"id"`
	Handle          string   `json:"handle"`
	Name            string   `json:"name"`
	AvatarURL       *string  `json:"avatarUrl"`
	BannerURL       *string  `json:"bannerUrl"`
	Description     string   `json:"description"`
	Tags            []string `json:"tags"`
	Visibility      string   `json:"visibility"`
	IsDefault       bool     `json:"isDefault"`
	SubscriberCount int64    `json:"subscriberCount"`
	VideoCount      *int64   `json:"videoCount,omitempty"`
	ShortCount      *int64   `json:"shortCount,omitempty"`
	LiveCount       *int64   `json:"liveCount,omitempty"`
	PostCount       *int64   `json:"postCount,omitempty"`
	Subscribed      *bool    `json:"subscribed,omitempty"`
	OwnerUsername   string   `json:"ownerUsername"`
}

type authResponseJSON struct {
	AccessToken  string          `json:"accessToken"`
	RefreshToken string          `json:"refreshToken"`
	User         userPrivateJSON `json:"user"`
}

func publicUser(u store.User) userPublicJSON {
	return userPublicJSON{
		ID:        u.ID.String(),
		Username:  u.Username,
		Nickname:  u.Nickname,
		AvatarURL: u.AvatarURL,
		Bio:       u.Bio,
		CreatedAt: u.CreatedAt.UTC().Format(time.RFC3339),
	}
}

func privateUser(u store.User, ch store.Channel, subscribed *bool) userPrivateJSON {
	return userPrivateJSON{
		userPublicJSON: publicUser(u),
		Email:          u.Email,
		EmailVerified:  u.EmailVerifiedAt != nil,
		DefaultChannel: channelJSONOf(ch, subscribed),
	}
}

func channelJSONOf(ch store.Channel, subscribed *bool) channelJSON {
	tags := ch.Tags
	if tags == nil {
		tags = []string{}
	}
	return channelJSON{
		ID:              ch.ID.String(),
		Handle:          ch.Handle,
		Name:            ch.Name,
		AvatarURL:       ch.AvatarURL,
		BannerURL:       ch.BannerURL,
		Description:     ch.Description,
		Tags:            tags,
		Visibility:      ch.Visibility,
		IsDefault:       ch.IsDefault,
		SubscriberCount: ch.SubscriberCount,
		Subscribed:      subscribed,
		OwnerUsername:   ch.OwnerUsername,
	}
}

func visibleTo(ch store.Channel, viewer *uuid.UUID) bool {
	switch ch.Visibility {
	case "public", "unlisted":
		return true
	case "private":
		return viewer != nil && *viewer == ch.OwnerUserID
	default:
		return false
	}
}

type cursor struct {
	T time.Time `json:"t"`
	I string    `json:"i"`
}

func encodeCursor(t time.Time, id string) string {
	b, _ := json.Marshal(cursor{T: t.UTC(), I: id})
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(s string) (time.Time, string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, "", false
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, "", false
	}
	var c cursor
	if err := json.Unmarshal(b, &c); err != nil {
		return time.Time{}, "", false
	}
	if c.I == "" {
		return time.Time{}, "", false
	}
	return c.T, c.I, true
}
