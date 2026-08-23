package application

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

var ErrAuthentication = errors.New("authentication failed")

const (
	argonMemory      uint32 = 64 * 1024
	argonIterations  uint32 = 3
	argonParallelism uint8  = 2
	argonSaltLength         = 16
	argonKeyLength   uint32 = 32
)

func HashPassword(password string, minimumLength int, random io.Reader) (string, error) {
	if !utf8.ValidString(password) || utf8.RuneCountInString(password) < minimumLength {
		return "", fmt.Errorf("password must contain at least %d characters", minimumLength)
	}
	if random == nil {
		random = rand.Reader
	}
	salt := make([]byte, argonSaltLength)
	if _, err := io.ReadFull(random, salt); err != nil {
		return "", err
	}
	hash := argon2.IDKey([]byte(password), salt, argonIterations, argonMemory, argonParallelism, argonKeyLength)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", argonMemory, argonIterations, argonParallelism,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(hash)), nil
}

func VerifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" {
		return false
	}
	var memory, iterations uint32
	var parallelism uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &parallelism); err != nil ||
		memory != argonMemory || iterations != argonIterations || parallelism != argonParallelism {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) != argonSaltLength {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) != int(argonKeyLength) {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, iterations, memory, parallelism, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

type BootstrapStore interface {
	UserCount(context.Context) (int, error)
	CreateFirstAdmin(context.Context, string, string, time.Time) (string, error)
}

func BootstrapAdmin(ctx context.Context, store BootstrapStore, username string, secretValue, secretFile string, minimumLength int, now time.Time) (bool, error) {
	count, err := store.UserCount(ctx)
	if err != nil {
		return false, err
	}
	if count != 0 {
		return false, nil
	}
	password := secretValue
	if password == "" {
		bytes, err := os.ReadFile(secretFile)
		if err != nil {
			return false, fmt.Errorf("read first-run admin secret: %w", err)
		}
		password = strings.TrimSuffix(strings.TrimSuffix(string(bytes), "\n"), "\r")
	}
	hash, err := HashPassword(password, minimumLength, rand.Reader)
	if err != nil {
		return false, err
	}
	if _, err := store.CreateFirstAdmin(ctx, username, hash, now.UTC()); err != nil {
		return false, err
	}
	return true, nil
}
