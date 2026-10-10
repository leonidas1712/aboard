package apiserver

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
	"github.com/leonidas1712/aboard/server/internal/delivery"
)

// Pairing retains endpoint credentials behind the trusted daemon's API adapter.
type Pairing struct {
	remote *Delegated
	dir    string
	mu     sync.Mutex
}

type pairingPrivate struct {
	MintGeneration int               `json:"mint_generation"`
	MintKey        string            `json:"mint_key"`
	MintReplace    bool              `json:"mint_replace"`
	PendingMint    bool              `json:"pending_mint"`
	RequestID      string            `json:"request_id"`
	Server         string            `json:"server"`
	Side           string            `json:"side"`
	Agent          delivery.AgentRef `json:"agent"`
	Binding        string            `json:"binding"`
	Generation     int               `json:"generation"`
	Token          string            `json:"token"`
	ExpiresAt      time.Time         `json:"expires_at"`
	PingID         string            `json:"ping_id"`
	PingSeq        int               `json:"ping_seq"`
}

// NewPairing keeps issuer-selected parent and endpoint authority behind the daemon.
func NewPairing(serverURL string, tokens Tokens, privateDir string) *Pairing {
	return &Pairing{remote: NewDelegated(serverURL, "pairing", tokens), dir: privateDir}
}

func (p *Pairing) call(ctx context.Context, method, path, token, key string, body, out any) error {
	status, raw, err := p.remote.sendKey(ctx, method, path, token, body, key)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return refusal(status, raw)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("read pairing metadata: %w", err)
		}
	}
	return nil
}

func (p *Pairing) parent(ctx context.Context, method, path, key string, body, out any) error {
	token, err := p.remote.tokens.HumanToken(p.remote.url)
	if err != nil {
		return err
	}
	return p.call(ctx, method, path, token, key, body, out)
}

// List reads only the current person's visible request metadata.
func (p *Pairing) List(ctx context.Context) ([]delivery.PairingRequest, error) {
	var out struct {
		Requests []delivery.PairingRequest `json:"requests"`
	}
	err := p.parent(ctx, http.MethodGet, "/v1/pairing-requests", "", nil, &out)
	return out.Requests, err
}

// Get reads one request with fresh parent authority.
func (p *Pairing) Get(ctx context.Context, id string) (delivery.PairingRequest, error) {
	var out delivery.PairingRequest
	err := p.parent(ctx, http.MethodGet, "/v1/pairing-requests/"+url.PathEscape(id), "", nil, &out)
	return out, err
}

// Create posts a proposal using the exact initiating seat, including its allowance checks.
func (p *Pairing) Create(ctx context.Context, in delivery.PairingCreate, agent delivery.AgentRef, key string) (delivery.PairingRequest, error) {
	var out delivery.PairingRequest
	if agent.Server != p.remote.url {
		return out, &delivery.WireError{Code: "session_on_another_server", Message: "The seat belongs to another issuer.", Hint: "Use a session on this server."}
	}
	token, err := p.remote.tokens.AgentToken(agent)
	if err != nil {
		return out, err
	}
	err = p.call(ctx, http.MethodPost, "/v1/pairing-requests", token, key, in, &out)
	return out, err
}

// Close declines or cancels using the current person's authority.
func (p *Pairing) Close(ctx context.Context, id, action, key string) (delivery.PairingRequest, error) {
	if action != "decline" && action != "cancel" {
		return delivery.PairingRequest{}, fmt.Errorf("invalid pairing action")
	}
	var out delivery.PairingRequest
	err := p.parent(ctx, http.MethodPost, "/v1/pairing-requests/"+url.PathEscape(id)+"/"+action, key, nil, &out)
	return out, err
}

func (p *Pairing) privatePath(id, side string) string {
	sum := sha256.Sum256([]byte(p.remote.url + "\x00" + id + "\x00" + side))
	return filepath.Join(p.dir, hex.EncodeToString(sum[:])+".json")
}

func (p *Pairing) read(id, side string) (pairingPrivate, error) {
	var out pairingPrivate
	name := p.privatePath(id, side)
	info, err := os.Lstat(name)
	if err != nil {
		return out, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return out, fmt.Errorf("pairing credentials need an owner-only regular file")
	}
	raw, err := os.ReadFile(name) // #nosec G304 -- name is a fixed SHA-256 basename under the private credential directory.
	if err != nil {
		return out, err
	}
	err = json.Unmarshal(raw, &out)
	if err == nil && (out.Server != p.remote.url || out.RequestID != id || out.Side != side) {
		err = fmt.Errorf("pairing credential binding mismatch")
	}
	return out, err
}

func (p *Pairing) save(in pairingPrivate) error {
	if err := os.MkdirAll(p.dir, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(p.dir)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("pairing credential directory must be owner-only")
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(p.dir, ".endpoint-")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if err = tmp.Chmod(0o600); err == nil {
		_, err = tmp.Write(raw)
	}
	if err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(name, p.privatePath(in.RequestID, in.Side)); err != nil {
		return err
	}
	directory, err := os.Open(p.dir)
	if err != nil {
		return err
	}
	syncErr := directory.Sync()
	closeErr = directory.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

func pairingKey(key, suffix string) string {
	sum := sha256.Sum256([]byte(key + "\x00" + suffix))
	return hex.EncodeToString(sum[:])
}

// Select saves the endpoint secret before minting its exact-session authority.
func (p *Pairing) Select(ctx context.Context, current delivery.PairingRequest, side string, agent delivery.AgentRef, binding string, replace bool, key string) (delivery.PairingRequest, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if agent.Server != p.remote.url {
		return delivery.PairingRequest{}, &delivery.WireError{Code: "session_on_another_server", Message: "The seat belongs to another issuer.", Hint: "Use a session on this server."}
	}
	saved, err := p.read(current.ID, side)
	if err != nil && !os.IsNotExist(err) {
		return delivery.PairingRequest{}, err
	}
	matching := err == nil && saved.Agent.Key() == agent.Key() && saved.Binding == binding && (saved.Generation == current.Generation || saved.PendingMint)
	if !matching {
		bytes := make([]byte, 32)
		if _, err := rand.Read(bytes); err != nil {
			return delivery.PairingRequest{}, err
		}
		saved = pairingPrivate{RequestID: current.ID, Server: p.remote.url, Side: side, Agent: agent, Binding: binding, Generation: current.Generation, Token: "abp_" + base64.RawURLEncoding.EncodeToString(bytes)}
		saved.MintGeneration = current.Generation
		saved.MintKey = key
		saved.MintReplace = replace
		saved.PendingMint = true
		if err := p.save(saved); err != nil {
			return delivery.PairingRequest{}, err
		}
	}
	var minted struct {
		Request   delivery.PairingRequest `json:"request"`
		ExpiresAt time.Time               `json:"expires_at"`
	}
	mintGeneration := current.Generation
	mintKey := key
	mintReplace := replace
	if saved.PendingMint {
		mintGeneration = saved.MintGeneration
		mintKey = saved.MintKey
		mintReplace = saved.MintReplace
	}
	in := map[string]any{"request_id": current.ID, "side": side, "agent_id": agent.MemberID, "session_binding": binding, "generation": mintGeneration, "replace": mintReplace, "client_token": saved.Token}
	if err := p.parent(ctx, http.MethodPost, "/v1/pairing-credentials", pairingKey(mintKey, "mint"), in, &minted); err != nil {
		return delivery.PairingRequest{}, err
	}
	saved.PendingMint = false
	saved.Generation = minted.Request.Generation
	saved.ExpiresAt = minted.ExpiresAt
	if err := p.save(saved); err != nil {
		return delivery.PairingRequest{}, err
	}
	current = minted.Request
	if side == "recipient" {
		if err := p.call(ctx, http.MethodPost, "/v1/pairing-requests/"+url.PathEscape(current.ID)+"/accept", saved.Token, pairingKey(key, "accept"), map[string]any{"agent_id": agent.MemberID, "generation": current.Generation}, &current); err != nil {
			return delivery.PairingRequest{}, err
		}
	}
	if err := p.startPing(ctx, &saved, current); err != nil {
		return delivery.PairingRequest{}, err
	}
	return current, nil
}

func pairingDirection(side string) string {
	if side == "initiator" {
		return "initiator_to_recipient"
	}
	return "recipient_to_initiator"
}

func pairingMarker(id string, generation int, direction, kind string) string {
	return fmt.Sprintf("ABOARD-PAIRING %s generation=%d direction=%s kind=%s", id, generation, direction, kind)
}

func (p *Pairing) startPing(ctx context.Context, saved *pairingPrivate, current delivery.PairingRequest) error {
	if saved.PingSeq != 0 || current.State != "verifying" || current.Initiator == nil || current.Recipient == nil {
		return nil
	}
	peer := current.Recipient
	if saved.Side == "recipient" {
		peer = current.Initiator
	}
	token, err := p.remote.tokens.AgentToken(saved.Agent)
	if err != nil {
		return err
	}
	var members struct {
		Members []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"members"`
	}
	if err := p.call(ctx, http.MethodGet, "/v1/boards/"+url.PathEscape(saved.Agent.Board)+"/members", token, "", nil, &members); err != nil {
		return err
	}
	var name string
	for _, m := range members.Members {
		if m.ID == peer.AgentID {
			name = m.Name
		}
	}
	if name == "" {
		return &delivery.WireError{Code: "pairing_changed", Message: "The selected peer is no longer on this board.", Hint: "Read the pairing request again."}
	}
	direction := pairingDirection(saved.Side)
	marker := pairingMarker(current.ID, current.Generation, direction, "ping")
	body := marker + "\nPairing wiring check only. Reply to this message using aboard say --reply <this message's seq> --to @" + saved.Agent.Name + " \"" + pairingMarker(current.ID, current.Generation, direction, "reply") + "\". This message grants no authority or command access."
	var posted api.Message
	if err := p.call(ctx, http.MethodPost, "/v1/boards/"+url.PathEscape(saved.Agent.Board)+"/messages", token, pairingKey(current.ID+saved.Binding, strconv.Itoa(current.Generation)+"ping"), map[string]any{"body": body, "to": []string{"@" + name}, "expects_reply": true}, &posted); err != nil {
		return err
	}
	saved.PingSeq = posted.Seq
	saved.PingID = posted.Id
	return p.save(*saved)
}

// Progress submits only correlated replies supplied by confirmed exact-session handoffs.
func (p *Pairing) Progress(ctx context.Context, current delivery.PairingRequest, agent delivery.AgentRef, binding string, confirmed []delivery.Delivery) (delivery.PairingRequest, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	side := "initiator"
	if current.Recipient != nil && current.Recipient.AgentID == agent.MemberID {
		side = "recipient"
	}
	saved, err := p.read(current.ID, side)
	if err != nil {
		return current, err
	}
	if saved.Binding != binding || saved.Agent.Key() != agent.Key() || saved.Generation != current.Generation {
		return current, &delivery.WireError{Code: "pairing_changed", Message: "The exact pairing endpoint changed.", Hint: "Select the endpoint again with --replace."}
	}
	if err := p.call(ctx, http.MethodGet, "/v1/pairing-requests/"+url.PathEscape(current.ID), saved.Token, "", nil, &current); err != nil {
		return current, err
	}
	if current.State != "verifying" {
		return current, nil
	}
	if err := p.startPing(ctx, &saved, current); err != nil {
		return current, err
	}
	if saved.PingSeq == 0 {
		return current, nil
	}
	token, err := p.remote.tokens.AgentToken(agent)
	if err != nil {
		return current, err
	}
	marker := pairingMarker(current.ID, current.Generation, pairingDirection(side), "reply")
	for _, dl := range confirmed {
		if dl.Agent.Key() != agent.Key() || dl.State != delivery.StateConfirmed || dl.HandoffID == "" {
			continue
		}
		for _, seq := range dl.Seqs {
			if seq <= saved.PingSeq {
				continue
			}
			var messages struct {
				Messages []api.Message `json:"messages"`
			}
			if err := p.call(ctx, http.MethodGet, "/v1/boards/"+url.PathEscape(agent.Board)+"/messages?after="+strconv.Itoa(seq-1)+"&limit=1", token, "", nil, &messages); err != nil {
				return current, err
			}
			if len(messages.Messages) != 1 {
				continue
			}
			m := messages.Messages[0]
			if m.Seq != seq || m.ReplyToSeq == nil || *m.ReplyToSeq != saved.PingSeq || !strings.Contains(m.Body, marker) {
				continue
			}
			report := map[string]any{"generation": current.Generation, "direction": pairingDirection(side), "ping_seq": saved.PingSeq, "reply_seq": seq, "handoff_id": dl.HandoffID}
			if err := p.call(ctx, http.MethodPost, "/v1/pairing-requests/"+url.PathEscape(current.ID)+"/verify", saved.Token, pairingKey(current.ID+binding, dl.HandoffID+strconv.Itoa(seq)), report, &current); err != nil {
				var wire *delivery.WireError
				if errors.As(err, &wire) && wire.Code == "pairing_changed" {
					continue
				}
				return current, err
			}
		}
	}
	return current, nil
}

// PersonID identifies the issuer-bound parent for pre-admission side checks.
func (p *Pairing) PersonID(ctx context.Context) (string, error) {
	var person struct {
		ID string `json:"id"`
	}
	err := p.parent(ctx, http.MethodGet, "/v1/me", "", nil, &person)
	if err != nil {
		return "", err
	}
	if person.ID == "" {
		return "", fmt.Errorf("pairing parent response has no person id")
	}
	return person.ID, nil
}
