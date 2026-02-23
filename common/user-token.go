package common

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"hash"
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

	return strings.TrimSpace(config.SessionSecret)
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
