package protocol

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const ContinuationPrefix = "elysia-continuation.v1."

// ContinuationRecord contains only the original signed fragment, not user
// history. Owner and parent are keyed digests; no credential is serialized.
type ContinuationRecord struct {
	Version   int      `json:"version"`
	ID        string   `json:"id"`
	Owner     string   `json:"owner"`
	Session   string   `json:"session,omitempty"`
	Scope     Scope    `json:"scope"`
	Protocol  Identity `json:"protocol"`
	Parent    string   `json:"parent"`
	Ordinal   int      `json:"ordinal"`
	Digest    string   `json:"digest"`
	Node      Node     `json:"node"`
	ExpiresAt int64    `json:"expiresAt"`
}

type ContinuationCodec struct {
	aead cipher.AEAD
	key  []byte
}

func NewContinuationCodec(master []byte) (*ContinuationCodec, error) {
	if len(master) == 0 {
		return nil, fmt.Errorf("continuation encryption requires the configured master key")
	}
	mac := hmac.New(sha256.New, master)
	mac.Write([]byte("elysia/protocol-continuation/v1"))
	key := mac.Sum(nil)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &ContinuationCodec{aead: aead, key: key}, nil
}

func (c *ContinuationCodec) Digest(value string) string {
	mac := hmac.New(sha256.New, c.key)
	mac.Write([]byte(value))
	return hex.EncodeToString(mac.Sum(nil))
}

func (c *ContinuationCodec) Seal(record ContinuationRecord, limit int) (string, error) {
	raw, err := json.Marshal(record)
	if err != nil {
		return "", err
	}
	if len(raw) > limit {
		return "", fmt.Errorf("continuation record exceeds byte limit")
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := c.aead.Seal(nonce, nonce, raw, []byte(ContinuationPrefix))
	return ContinuationPrefix + base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (c *ContinuationCodec) Open(token string, limit int) (ContinuationRecord, error) {
	var record ContinuationRecord
	if !strings.HasPrefix(token, ContinuationPrefix) || len(token) > 2*limit+256 {
		return record, fmt.Errorf("invalid continuation envelope")
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(token, ContinuationPrefix))
	if err != nil || len(raw) < c.aead.NonceSize() {
		return record, fmt.Errorf("invalid continuation encoding")
	}
	clear, err := c.aead.Open(nil, raw[:c.aead.NonceSize()], raw[c.aead.NonceSize():], []byte(ContinuationPrefix))
	if err != nil {
		return record, fmt.Errorf("continuation authentication failed")
	}
	if len(clear) > limit {
		return record, fmt.Errorf("continuation record exceeds byte limit")
	}
	if err = decodeContract(clear, &record); err != nil {
		return record, err
	}
	if record.Version != 1 || record.ExpiresAt <= time.Now().Unix() {
		return record, fmt.Errorf("continuation is expired or has an unsupported version")
	}
	return record, nil
}

func NewContinuationID() (string, error) {
	var raw [24]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

// ContinuationNodeDigest excludes adapter provenance and presentation IDs.
// Actual text, media, arguments and call identity must match exactly.
func ContinuationNodeDigest(node Node) string {
	node.ID, node.Status, node.Role = Value{}, Value{}, Value{}
	node.Source, node.Native = nil, nil
	node.Resources = nil
	node.Cache = nil
	node.Attributes = nil
	if len(node.Children) > 0 {
		children := cloneNodes(node.Children)
		var clean func([]Node)
		clean = func(nodes []Node) {
			for i := range nodes {
				n := &nodes[i]
				n.ID, n.Status, n.Role = Value{}, Value{}, Value{}
				n.Source, n.Native, n.Resources, n.Cache, n.Attributes = nil, nil, nil, nil, nil
				clean(n.Children)
			}
		}
		clean(children)
		node.Children = children
	}
	value, _ := EncodeValue(node)
	return hashValue(value)
}

func HasSignature(node Node) bool {
	for _, r := range node.Resources {
		if r.Kind == "signature" {
			return true
		}
	}
	return false
}

// RestoreContinuation attaches only authenticated state to an unchanged owner.
// Never replace visible client content with a stored "similar" reply.
func RestoreContinuation(node *Node, record ContinuationRecord, owner string, scope Scope, target Identity) error {
	if record.Owner != owner || !CheckScope(record.Scope, scope) || record.Protocol.Family != target.Family || record.Protocol.WireVersion != target.WireVersion {
		return fmt.Errorf("continuation owner, account, model or protocol differs")
	}
	if ContinuationNodeDigest(*node) != record.Digest {
		return fmt.Errorf("continuation content changed")
	}
	if HasSignature(*node) {
		return nil
	}
	for _, r := range record.Node.Resources {
		if r.Kind == "signature" {
			node.Resources = append(node.Resources, r)
		}
	}
	node.Source = record.Node.Source
	node.Native = record.Node.Native
	return nil
}
