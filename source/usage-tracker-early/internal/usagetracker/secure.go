package usagetracker

import (
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"

	"filippo.io/age"
)

var deviceID = regexp.MustCompile(`^[0-9a-f]{32}$`)
var repoName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9_.-]+$`)

const pairPrefix = "UCT-A1-"
const devicePrefix = "UCT-B1-"
const dayFormat = "UCT-D1\x00"
const policyFormat = "UCT-P1\x00"
const maxDay = 48 << 20

type Pair struct {
	Repo      string `json:"repo"`
	Recipient string `json:"recipient"`
	Verifier  []byte `json:"verifier"`
}

type Device struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Verifier []byte `json:"verifier"`
}

func (p Pair) valid() bool {
	if !repoName.MatchString(p.Repo) || len(p.Verifier) != ed25519.PublicKeySize {
		return false
	}
	_, err := age.ParseX25519Recipient(p.Recipient)
	return err == nil
}

func (d Device) valid() bool {
	return len(d.Verifier) == ed25519.PublicKeySize && d.ID == digest("device", string(d.Verifier)) &&
		len(d.Name) > 0 && len(d.Name) <= 128
}

func code(prefix string, payload any) (string, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(body), nil
}

func parseCode(input, prefix string, value any) error {
	body, ok := strings.CutPrefix(strings.TrimSpace(input), prefix)
	if !ok || len(body) > 8192 {
		return errors.New("配对码格式不正确")
	}
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil || json.Unmarshal(raw, value) != nil {
		return errors.New("配对码格式不正确")
	}
	return nil
}

func makeKeys(role string) (string, ed25519.PrivateKey, error) {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil || role != "a" {
		return "", key, err
	}
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		return "", nil, err
	}
	return identity.String(), key, nil
}

func sealDay(day Day, recipient string, key ed25519.PrivateKey) ([]byte, error) {
	to, err := age.ParseX25519Recipient(recipient)
	if err != nil {
		return nil, err
	}
	var sealed bytes.Buffer
	writer, err := age.Encrypt(&sealed, to)
	if err != nil {
		return nil, err
	}
	compressed := gzip.NewWriter(writer)
	if err := json.NewEncoder(compressed).Encode(day); err != nil {
		return nil, err
	}
	if err := compressed.Close(); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return sign(dayFormat, sealed.Bytes(), key), nil
}

func openDay(blob []byte, identity string, device Device, date string) (Day, error) {
	var day Day
	ciphertext, err := verify(dayFormat, blob, device.Verifier)
	if err != nil {
		return day, err
	}
	key, err := age.ParseX25519Identity(identity)
	if err != nil {
		return day, err
	}
	raw, err := age.Decrypt(bytes.NewReader(ciphertext), key)
	if err != nil {
		return day, err
	}
	compressed, err := gzip.NewReader(raw)
	if err != nil {
		return day, err
	}
	defer compressed.Close()
	data, err := io.ReadAll(io.LimitReader(compressed, maxDay+1))
	if err != nil {
		return day, err
	}
	if len(data) > maxDay || json.Unmarshal(data, &day) != nil {
		return day, errors.New("用量文件格式或大小不正确")
	}
	if day.Device != device.ID || day.Date != date || day.At.IsZero() {
		return day, errors.New("用量文件与设备或日期不匹配")
	}
	return day, nil
}

func sign(format string, content []byte, key ed25519.PrivateKey) []byte {
	body := append([]byte(format), content...)
	return append(ed25519.Sign(key, body), body...)
}

func verify(format string, blob, verifier []byte) ([]byte, error) {
	if len(verifier) != ed25519.PublicKeySize || len(blob) < ed25519.SignatureSize+len(format) {
		return nil, errors.New("文件签名格式不正确")
	}
	body := blob[ed25519.SignatureSize:]
	if !bytes.HasPrefix(body, []byte(format)) || !ed25519.Verify(verifier, body, blob[:ed25519.SignatureSize]) {
		return nil, errors.New("文件签名验证失败")
	}
	return body[len(format):], nil
}
