package qobuz

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// User is the account a token belongs to, as user/get describes it.
type User struct {
	ID          string
	Email       string
	DisplayName string
	Country     string

	// Offer is the plan ("studio", "sublime", ...) and EndDate when it runs out; Canceled
	// means it will not renew. Credential is the label Qobuz puts on what the account may
	// stream ("Qobuz Studio").
	Offer      string
	EndDate    string
	Canceled   bool
	Credential string
}

// User returns the account the token belongs to. It is how a token is checked: Qobuz answers
// 401 to a token it does not accept, and the account's plan says whether hi-res will stream.
func (c *Client) User(ctx context.Context) (*User, error) {
	if c.authToken == "" {
		return nil, ErrNoAuthToken
	}
	var response struct {
		ID           flexString `json:"id"`
		Email        string     `json:"email"`
		DisplayName  string     `json:"display_name"`
		Country      string     `json:"country_code"`
		Subscription *struct {
			Offer    string `json:"offer"`
			EndDate  string `json:"end_date"`
			Canceled bool   `json:"is_canceled"`
		} `json:"subscription"`
		Credential *struct {
			Label       string `json:"label"`
			Description string `json:"description"`
		} `json:"credential"`
	}
	if err := c.get(ctx, "user/get", url.Values{}, &response); err != nil {
		return nil, err
	}
	user := &User{
		ID:          string(response.ID),
		Email:       response.Email,
		DisplayName: response.DisplayName,
		Country:     response.Country,
	}
	if response.Subscription != nil {
		user.Offer = response.Subscription.Offer
		user.EndDate = response.Subscription.EndDate
		user.Canceled = response.Subscription.Canceled
	}
	if response.Credential != nil {
		user.Credential = orDefault(response.Credential.Description, response.Credential.Label)
	}
	return user, nil
}

// Track returns one track with the album it belongs to.
func (c *Client) Track(ctx context.Context, id string) (*Track, error) {
	if strings.TrimSpace(id) == "" {
		return nil, errors.New("qobuz: empty track id")
	}
	var item trackJSON
	if err := c.get(ctx, "track/get", url.Values{"track_id": {id}}, &item); err != nil {
		return nil, err
	}
	track := item.track()
	if track.ID == "" {
		track.ID = id
	}
	if track.Album == nil {
		return nil, fmt.Errorf("qobuz: track %s came back without its album", id)
	}
	return &track, nil
}

// SearchTracks returns tracks matching a free-text query, each with its album.
func (c *Client) SearchTracks(ctx context.Context, query string, limit int) ([]Track, error) {
	if strings.TrimSpace(query) == "" {
		return nil, errors.New("qobuz: empty search query")
	}
	if limit <= 0 {
		limit = 10
	}
	var response struct {
		Tracks struct {
			Items []trackJSON `json:"items"`
		} `json:"tracks"`
	}
	if err := c.get(ctx, "track/search", url.Values{
		"query":  {query},
		"limit":  {strconv.Itoa(limit)},
		"offset": {"0"},
	}, &response); err != nil {
		return nil, err
	}
	tracks := make([]Track, 0, len(response.Tracks.Items))
	for _, item := range response.Tracks.Items {
		if item.ID == "" {
			continue
		}
		tracks = append(tracks, item.track())
	}
	return tracks, nil
}

// Artist is a Qobuz artist with every album the store lists under them. The albums carry
// no track listing; Album reads one.
type Artist struct {
	ID     string
	Name   string
	Albums []Album
}

// Artist returns an artist and their whole discography, paging through it.
func (c *Client) Artist(ctx context.Context, id string) (*Artist, error) {
	if strings.TrimSpace(id) == "" {
		return nil, errors.New("qobuz: empty artist id")
	}
	artist := &Artist{ID: id}
	for offset := 0; ; offset += pageLimit {
		var response struct {
			Name   string `json:"name"`
			Albums *struct {
				Total int         `json:"total"`
				Items []albumJSON `json:"items"`
			} `json:"albums"`
		}
		if err := c.get(ctx, "artist/get", url.Values{
			"artist_id": {id},
			"extra":     {"albums"},
			"limit":     {strconv.Itoa(pageLimit)},
			"offset":    {strconv.Itoa(offset)},
		}, &response); err != nil {
			return nil, err
		}
		artist.Name = orDefault(artist.Name, response.Name)
		if response.Albums == nil {
			break
		}
		for _, item := range response.Albums.Items {
			if item.ID != "" {
				artist.Albums = append(artist.Albums, item.album())
			}
		}
		if len(response.Albums.Items) == 0 || offset+len(response.Albums.Items) >= response.Albums.Total {
			break
		}
	}
	return artist, nil
}

// SearchArtists returns artists matching a free-text query, without their albums.
func (c *Client) SearchArtists(ctx context.Context, query string, limit int) ([]Artist, error) {
	if strings.TrimSpace(query) == "" {
		return nil, errors.New("qobuz: empty search query")
	}
	if limit <= 0 {
		limit = 10
	}
	var response struct {
		Artists struct {
			Items []struct {
				ID   flexString `json:"id"`
				Name string     `json:"name"`
			} `json:"items"`
		} `json:"artists"`
	}
	if err := c.get(ctx, "artist/search", url.Values{
		"query":  {query},
		"limit":  {strconv.Itoa(limit)},
		"offset": {"0"},
	}, &response); err != nil {
		return nil, err
	}
	artists := make([]Artist, 0, len(response.Artists.Items))
	for _, item := range response.Artists.Items {
		if item.ID != "" {
			artists = append(artists, Artist{ID: string(item.ID), Name: item.Name})
		}
	}
	return artists, nil
}

// Label is a record label with every album the store lists under it.
type Label struct {
	ID     string
	Name   string
	Albums []Album
}

// Label returns a label and its whole catalogue, paging through it.
func (c *Client) Label(ctx context.Context, id string) (*Label, error) {
	if strings.TrimSpace(id) == "" {
		return nil, errors.New("qobuz: empty label id")
	}
	label := &Label{ID: id}
	for offset := 0; ; offset += pageLimit {
		var response struct {
			Name   string `json:"name"`
			Albums *struct {
				Total int         `json:"total"`
				Items []albumJSON `json:"items"`
			} `json:"albums"`
		}
		if err := c.get(ctx, "label/get", url.Values{
			"label_id": {id},
			"extra":    {"albums"},
			"limit":    {strconv.Itoa(pageLimit)},
			"offset":   {strconv.Itoa(offset)},
		}, &response); err != nil {
			return nil, err
		}
		label.Name = orDefault(label.Name, response.Name)
		if response.Albums == nil {
			break
		}
		for _, item := range response.Albums.Items {
			if item.ID != "" {
				label.Albums = append(label.Albums, item.album())
			}
		}
		if len(response.Albums.Items) == 0 || offset+len(response.Albums.Items) >= response.Albums.Total {
			break
		}
	}
	return label, nil
}

// Playlist is a Qobuz playlist with its tracks in playlist order, each carrying its album.
type Playlist struct {
	ID     string
	Name   string
	Owner  string
	Tracks []Track
}

// Playlist returns a playlist and all of its tracks, paging through them.
func (c *Client) Playlist(ctx context.Context, id string) (*Playlist, error) {
	if strings.TrimSpace(id) == "" {
		return nil, errors.New("qobuz: empty playlist id")
	}
	playlist := &Playlist{ID: id}
	for offset := 0; ; offset += pageLimit {
		var response struct {
			Name   string `json:"name"`
			Owner  *named `json:"owner"`
			Tracks *struct {
				Total int         `json:"total"`
				Items []trackJSON `json:"items"`
			} `json:"tracks"`
		}
		if err := c.get(ctx, "playlist/get", url.Values{
			"playlist_id": {id},
			"extra":       {"tracks"},
			"limit":       {strconv.Itoa(pageLimit)},
			"offset":      {strconv.Itoa(offset)},
		}, &response); err != nil {
			return nil, err
		}
		playlist.Name = orDefault(playlist.Name, response.Name)
		playlist.Owner = orDefault(playlist.Owner, response.Owner.name())
		if response.Tracks == nil {
			break
		}
		for _, item := range response.Tracks.Items {
			if item.ID != "" {
				playlist.Tracks = append(playlist.Tracks, item.track())
			}
		}
		if len(response.Tracks.Items) == 0 || offset+len(response.Tracks.Items) >= response.Tracks.Total {
			break
		}
	}
	return playlist, nil
}

// SearchPlaylists returns playlists matching a free-text query, without their tracks.
func (c *Client) SearchPlaylists(ctx context.Context, query string, limit int) ([]Playlist, error) {
	if strings.TrimSpace(query) == "" {
		return nil, errors.New("qobuz: empty search query")
	}
	if limit <= 0 {
		limit = 10
	}
	var response struct {
		Playlists struct {
			Items []struct {
				ID    flexString `json:"id"`
				Name  string     `json:"name"`
				Owner *named     `json:"owner"`
			} `json:"items"`
		} `json:"playlists"`
	}
	if err := c.get(ctx, "playlist/search", url.Values{
		"query":  {query},
		"limit":  {strconv.Itoa(limit)},
		"offset": {"0"},
	}, &response); err != nil {
		return nil, err
	}
	playlists := make([]Playlist, 0, len(response.Playlists.Items))
	for _, item := range response.Playlists.Items {
		if item.ID != "" {
			playlists = append(playlists, Playlist{ID: string(item.ID), Name: item.Name, Owner: item.Owner.name()})
		}
	}
	return playlists, nil
}
