package api

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	ConfigDirEnv = "LANGQUAIL_CONFIG_DIR"
	tokenPrefix  = "lq_"
)

type tokenFile struct {
	Version   int       `json:"version"`
	Token     string    `json:"token"`
	CreatedAt time.Time `json:"created_at"`
}

func loadOrCreateToken(configDir string) (string, string, error) {
	dir, err := resolveConfigDir(configDir)
	if err != nil {
		return "", "", err
	}
	return loadOrCreateTokenPath(filepath.Join(dir, "server.json"))
}

func loadOrCreateTokenFile(path string) (string, string, error) {
	if strings.TrimSpace(path) == "" {
		return "", "", errors.New("api: token file path is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", "", err
	}
	return loadOrCreateTokenPath(abs)
}

func loadOrCreateTokenPath(path string) (string, string, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", err
	}
	if _, err := os.Stat(path); err == nil {
		token, err := readTokenFile(path)
		return token, path, err
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", "", err
	}

	token, err := generateToken()
	if err != nil {
		return "", "", err
	}
	record := tokenFile{
		Version:   1,
		Token:     token,
		CreatedAt: time.Now().UTC(),
	}
	raw, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return "", "", err
	}
	raw = append(raw, '\n')
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", "", err
	}
	defer file.Close()
	if _, err := file.Write(raw); err != nil {
		return "", "", err
	}
	return token, path, nil
}

func resolveConfigDir(configDir string) (string, error) {
	if configDir != "" {
		return filepath.Abs(configDir)
	}
	if override := os.Getenv(ConfigDirEnv); override != "" {
		return filepath.Abs(override)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "langquail"), nil
}

func readTokenFile(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var record tokenFile
	if err := json.Unmarshal(raw, &record); err != nil {
		return "", fmt.Errorf("api: invalid token file %s: %w", path, err)
	}
	if record.Version != 1 {
		return "", fmt.Errorf("api: unsupported token file version %d", record.Version)
	}
	if !validToken(record.Token) {
		return "", fmt.Errorf("api: invalid token in %s", path)
	}
	return record.Token, nil
}

func generateToken() (string, error) {
	var bytes [32]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return tokenPrefix + base64.RawURLEncoding.EncodeToString(bytes[:]), nil
}

func validToken(token string) bool {
	if !strings.HasPrefix(token, tokenPrefix) {
		return false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(token, tokenPrefix))
	return err == nil && len(decoded) == 32
}

func (s *Server) authorized(r *http.Request) bool {
	token := tokenFromRequest(r)
	if token == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(s.token)) == 1
}

func tokenFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	if header := r.Header.Get("Authorization"); header != "" {
		parts := strings.SplitN(header, " ", 2)
		if len(parts) == 2 && strings.EqualFold(parts[0], "bearer") {
			return strings.TrimSpace(parts[1])
		}
	}
	if token := strings.TrimSpace(r.Header.Get("X-LangQuail-Token")); token != "" {
		return token
	}
	return strings.TrimSpace(r.URL.Query().Get("token"))
}
