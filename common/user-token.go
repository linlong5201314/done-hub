package common

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"hash"
	"log"
	"os"
	"strings"
	"sync"

	"done-hub/common/config"

	"github.com/spf13/viper"
	"github.com/sqids/sqids-go"
)

var (
	hashidsMinLength = 15
	hashids          *sqids.Sqids

	jwtSecretBytes = []byte{}
	hmacPool       = sync.Pool{
		New: func() interface{} {
			return hmac.New(sha256.New, jwtSecretBytes)
		},
	}

	secretFileName = ".user_token_secret"
)

func InitUserToken() error {
	tokenSecret := resolveUserTokenSecret()
	sqidsAlphabet := strings.TrimSpace(viper.GetString("hashids_salt"))

	if tokenSecret == "" {
		return errors.New("user_token_secret, token_secret and session_secret are all empty")
	}

	var err error

	sqidsOptions := sqids.Options{
		MinLength: uint8(hashidsMinLength),
	}

	if sqidsAlphabet != "" {
		sqidsOptions.Alphabet = sqidsAlphabet
	}

	hashids, err = sqids.New(sqidsOptions)

	jwtSecretBytes = []byte(tokenSecret)

	return err
}

func resolveUserTokenSecret() string {
	for _, key := range []string{"user_token_secret", "token_secret", "session_secret"} {
		if secret := strings.TrimSpace(viper.GetString(key)); secret != "" {
			return secret
		}
	}

	// No environment variable set, try to load persisted secret from file
	if data, err := os.ReadFile(secretFileName); err == nil {
		if secret := strings.TrimSpace(string(data)); secret != "" {
			log.Printf("[WARNING] No USER_TOKEN_SECRET or SESSION_SECRET env set, using persisted secret from %s", secretFileName)
			return secret
		}
	}

	// Fall back to config.SessionSecret (random UUID) and persist it for next restart
	secret := strings.TrimSpace(config.SessionSecret)
	if secret == "" {
		return ""
	}

	if err := os.WriteFile(secretFileName, []byte(secret), 0600); err != nil {
		log.Printf("[WARNING] Failed to persist token secret to %s: %v — tokens will be invalidated on restart!", secretFileName, err)
	} else {
		log.Printf("[WARNING] No USER_TOKEN_SECRET or SESSION_SECRET env set. Auto-generated secret persisted to %s. Set a fixed secret in production.", secretFileName)
	}

	return secret
}

func GenerateToken(tokenID, userID int) (string, error) {
	payload, err := hashids.Encode([]uint64{uint64(tokenID), uint64(userID)})
	if err != nil {
		return "", err
	}

	h := hmacPool.Get().(hash.Hash)
	defer func() {
		h.Reset()
		hmacPool.Put(h)
	}()

	h.Write([]byte(payload))
	signature := base64.RawURLEncoding.EncodeToString(h.Sum(nil))

	return payload + "_" + signature, nil
}

func ValidateToken(token string) (tokenID, userID int, err error) {
	parts := bytes.SplitN([]byte(token), []byte("_"), 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("无效的令牌")
	}

	payloadEncoded, receivedSignature := parts[0], parts[1]

	h := hmacPool.Get().(hash.Hash)
	defer func() {
		h.Reset()
		hmacPool.Put(h)
	}()

	h.Write(payloadEncoded)
	expectedSignature := h.Sum(nil)

	decodedSignature, err := base64.RawURLEncoding.DecodeString(string(receivedSignature))
	if err != nil {
		return 0, 0, fmt.Errorf("签名解码失败")
	}

	if !bytes.Equal(decodedSignature, expectedSignature) {
		return 0, 0, fmt.Errorf("签名验证失败")
	}

	numbers := hashids.Decode(string(payloadEncoded))
	if len(numbers) != 2 {
		return 0, 0, fmt.Errorf("无效的令牌")
	}

	return int(numbers[0]), int(numbers[1]), nil
}
