package clawnify

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// QueueError is ClawnifyQueueError: Status is 0 when the call never left.
type QueueError struct {
	Message string
	Status  int
}

func (e *QueueError) Error() string { return e.Message }

const defaultQueueURL = "https://services.clawnify.com/queue"

// queueClient and jwksURL are variables so tests can point them at fakes.
var (
	queueClient = http.DefaultClient
	jwksURL     = "https://services.clawnify.com/.well-known/jwks.json"
)

// EnqueueOptions are the fields of a queued job. Zero values are left out of
// the request, as undefined ones were.
type EnqueueOptions struct {
	TargetURL      string
	Payload        any
	RunAt          time.Time
	IdempotencyKey string
	MaxAttempts    int
}

// Job is what the queue answers with.
type Job struct {
	ID string `json:"id"`
}

func queueBase(env Env) (string, error) {
	if env.Token == "" {
		return "", &QueueError{Message: "CLAWNIFY_TOKEN is not set — the queue service is unavailable."}
	}
	if env.QueueURL == "" {
		return defaultQueueURL, nil
	}
	return trimSlashes(env.QueueURL), nil
}

func queueCall(ctx context.Context, env Env, method, path string, body any, out any) error {
	base, err := queueBase(env)
	if err != nil {
		return err
	}
	status, raw, err := doJSON(ctx, queueClient, method, base+path, map[string]string{
		"Authorization": "Bearer " + env.Token,
		"Content-Type":  "application/json",
	}, body)
	if err != nil {
		return err
	}
	var parsed map[string]any
	if len(raw) > 0 {
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			return err // JSON.parse threw on it
		}
		parsed, _ = v.(map[string]any)
	}
	if status < 200 || status > 299 {
		msg, _ := parsed["detail"].(string)
		if msg == "" {
			msg, _ = parsed["error"].(string)
		}
		if msg == "" {
			msg = fmt.Sprintf("queue service %d", status)
		}
		return &QueueError{Message: msg, Status: status}
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// Enqueue schedules a delivery of Payload to TargetURL (enqueueJob).
func Enqueue(ctx context.Context, env Env, o EnqueueOptions) (Job, error) {
	body := map[string]any{"target_url": o.TargetURL}
	if o.Payload != nil {
		body["payload"] = o.Payload
	}
	if !o.RunAt.IsZero() {
		body["run_at"] = o.RunAt.UTC().Format("2006-01-02T15:04:05.000Z")
	}
	if o.IdempotencyKey != "" {
		body["idempotency_key"] = o.IdempotencyKey
	}
	if o.MaxAttempts != 0 {
		body["max_attempts"] = o.MaxAttempts
	}
	var job Job
	err := queueCall(ctx, env, "POST", "/enqueue", body, &job)
	return job, err
}

// CancelJob drops a queued job.
func CancelJob(ctx context.Context, env Env, id string) error {
	return queueCall(ctx, env, "DELETE", "/jobs/"+id, nil, nil)
}

type jwk struct {
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
	Kid string `json:"kid"`
}

var (
	jwksMu    sync.Mutex
	jwksCache = map[string][]jwk{}
)

// fetchJWKS caches the key set per URL for the life of the process, as the TS
// client did.
func fetchJWKS(ctx context.Context, url string) ([]jwk, error) {
	jwksMu.Lock()
	defer jwksMu.Unlock()
	if keys, ok := jwksCache[url]; ok {
		return keys, nil
	}
	status, raw, err := doJSON(ctx, queueClient, "GET", url, nil, nil)
	if err != nil {
		return nil, err
	}
	if status < 200 || status > 299 {
		return nil, &QueueError{Message: fmt.Sprintf("jwks fetch %d", status), Status: status}
	}
	var set struct {
		Keys []jwk `json:"keys"`
	}
	if err := json.Unmarshal(raw, &set); err != nil {
		return nil, err
	}
	jwksCache[url] = set.Keys
	return set.Keys, nil
}

const deliveryTolerance = 300 // seconds

// VerifyDelivery checks that a delivery came from the queue: an ECDSA P-256
// signature over "<timestamp>.<rawBody>", made within five minutes. Anything
// wrong, including an unreachable key set, is false.
func VerifyDelivery(ctx context.Context, rawBody, signature, timestamp, keyID string) bool {
	if signature == "" || timestamp == "" {
		return false
	}
	ts, ok := jsNumber(timestamp)
	if !ok {
		return false
	}
	if math.Abs(float64(time.Now().UnixMilli())/1e3-ts) > deliveryTolerance {
		return false
	}
	keys, err := fetchJWKS(ctx, jwksURL)
	if err != nil {
		return false
	}
	var key *jwk
	for i := range keys {
		if keyID == "" || keys[i].Kid == keyID {
			key = &keys[i]
			break
		}
	}
	if key == nil && len(keys) > 0 {
		key = &keys[0]
	}
	if key == nil || key.Kty != "EC" || key.Crv != "P-256" {
		return false
	}
	x, err1 := base64.RawURLEncoding.DecodeString(strings.TrimRight(key.X, "="))
	y, err2 := base64.RawURLEncoding.DecodeString(strings.TrimRight(key.Y, "="))
	sig, err3 := base64.StdEncoding.DecodeString(signature)
	if err := errors.Join(err1, err2, err3); err != nil || len(sig) != 64 {
		return false
	}
	pub := &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}
	if !pub.Curve.IsOnCurve(pub.X, pub.Y) {
		return false
	}
	// The message uses the timestamp as a number, so "01" signs as "1".
	msg := sha256.Sum256([]byte(formatTS(ts) + "." + rawBody))
	return ecdsa.Verify(pub, msg[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:]))
}

// jsNumber is Number(s) for the finite case; empty and whitespace-only strings
// are excluded by the caller and by the parse.
func jsNumber(s string) (float64, bool) {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
		return 0, false
	}
	return f, true
}

// formatTS is `${ts}` for a JS number.
func formatTS(f float64) string {
	if f == math.Trunc(f) && math.Abs(f) < 1e21 {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}
