package qobuz

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
)

// The web player publishes the app id and the pieces of the signing secrets in its own
// JavaScript bundle. Qobuz rotates both, so they are read from the bundle on first use
// rather than configured.
var (
	bundlePattern     = regexp.MustCompile(`(?i)<script src="(/resources/[^"]+bundle\.js)"></script>`)
	appIDPattern      = regexp.MustCompile(`production:\{api:\{appId:"(\d{9})",appSecret:"\w{32}"`)
	seedPattern       = regexp.MustCompile(`(?i)[a-z]\.initialSeed\("([\w=]+)",window\.utimezone\.([a-z]+)\)`)
	infoExtrasPattern = `(?i)name:"\w+/(%s)",info:"([\w=]+)",extras:"([\w=]+)"`
)

// secretTailLength is how much of each assembled secret is padding the player appends.
// Dropping it is what leaves valid base64 behind.
const secretTailLength = 44

// bundle is what the player publishes: the app id every request needs, and the secrets a
// stream URL is signed with.
type bundle struct {
	appID   string
	secrets []string
}

// fetchBundle reads the app id and the signing secrets out of the web player.
//
// Each secret is assembled from three fragments the bundle keeps apart, one set per
// timezone. Which set signs successfully depends on the account, so all of them are kept
// and tried in turn; the first two are swapped because the player itself prefers the
// second.
func fetchBundle(ctx context.Context, client *http.Client, playURL, userAgent string) (*bundle, error) {
	login, err := getText(ctx, client, playURL+"/login", userAgent)
	if err != nil {
		return nil, fmt.Errorf("qobuz: fetch login page: %w", err)
	}

	path := bundlePattern.FindStringSubmatch(login)
	if len(path) < 2 {
		return nil, fmt.Errorf("qobuz: no player bundle in %s/login", playURL)
	}

	js, err := getText(ctx, client, playURL+path[1], userAgent)
	if err != nil {
		return nil, fmt.Errorf("qobuz: fetch player bundle: %w", err)
	}

	appID := appIDPattern.FindStringSubmatch(js)
	if len(appID) < 2 {
		return nil, fmt.Errorf("qobuz: no app id in player bundle %s", path[1])
	}

	secrets, err := extractSecrets(js)
	if err != nil {
		return nil, err
	}
	return &bundle{appID: appID[1], secrets: secrets}, nil
}

// fragments are the three parts of one timezone's secret, in the order they concatenate.
type fragments struct {
	timezone string
	seed     string
	info     string
	extras   string
}

func extractSecrets(js string) ([]string, error) {
	var (
		sets  []fragments
		known = map[string]int{}
	)
	for _, m := range seedPattern.FindAllStringSubmatch(js, -1) {
		timezone := strings.ToLower(m[2])
		if at, seen := known[timezone]; seen {
			sets[at].seed += m[1]
			continue
		}
		known[timezone] = len(sets)
		sets = append(sets, fragments{timezone: timezone, seed: m[1]})
	}
	if len(sets) == 0 {
		return nil, fmt.Errorf("qobuz: no secret seeds in player bundle")
	}
	if len(sets) > 1 {
		sets[0], sets[1] = sets[1], sets[0]
	}

	quoted := make([]string, 0, len(sets))
	for _, set := range sets {
		quoted = append(quoted, regexp.QuoteMeta(set.timezone))
	}
	pattern, err := regexp.Compile(fmt.Sprintf(infoExtrasPattern, strings.Join(quoted, "|")))
	if err != nil {
		return nil, fmt.Errorf("qobuz: build secret pattern: %w", err)
	}

	for _, m := range pattern.FindAllStringSubmatch(js, -1) {
		// The swap above invalidated the positions in known, so match on the timezone.
		timezone := strings.ToLower(m[1])
		for i := range sets {
			if sets[i].timezone == timezone {
				sets[i].info, sets[i].extras = m[2], m[3]
				break
			}
		}
	}

	var secrets []string
	for _, set := range sets {
		assembled := set.seed + set.info + set.extras
		if len(assembled) <= secretTailLength {
			continue
		}
		decoded, err := decodeBase64(assembled[:len(assembled)-secretTailLength])
		if err != nil {
			// One unusable timezone is normal; another one usually signs.
			continue
		}
		secrets = append(secrets, string(decoded))
	}
	if len(secrets) == 0 {
		return nil, fmt.Errorf("qobuz: no usable secret in player bundle")
	}
	return secrets, nil
}

// decodeBase64 decodes a fragment the player left unpadded.
func decodeBase64(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if remainder := len(s) % 4; remainder > 0 {
		s += strings.Repeat("=", 4-remainder)
	}
	return base64.StdEncoding.DecodeString(s)
}

func getText(ctx context.Context, client *http.Client, url, userAgent string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s returned %d", url, resp.StatusCode)
	}

	// The bundle is several megabytes; the limit is there to stop a redirect to something
	// unbounded, not to bound the bundle itself.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return "", err
	}
	return string(body), nil
}
