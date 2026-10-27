package daemon

// machinekey.go: the daemon's data-dir-scoped Ed25519 identity
// (workbench#11). <data-dir>/machine-key holds the hex seed, created once
// atomically (temp file + fsync + link + directory fsync, under the
// data-dir flock) and reused forever. A corrupt or unreadable file refuses
// the start — never a silent regeneration (that would silently re-register
// a new identity). An empty file is the one exception: it is the residue of
// an interrupted first create under the pre-atomic path and never held an
// identity, so it is created once more.

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const machineKeyFile = "machine-key"

// loadOrCreateMachineKey answers the daemon key and its base64 public half.
func loadOrCreateMachineKey(dataDir string) (ed25519.PrivateKey, string, error) {
	path := filepath.Join(dataDir, machineKeyFile)
	raw, err := os.ReadFile(path)
	if err == nil {
		if strings.TrimSpace(string(raw)) == "" {
			return createMachineKey(dataDir, path, true)
		}
		return parseMachineKey(path, raw)
	}
	if !os.IsNotExist(err) {
		return nil, "", fmt.Errorf("read machine key %s: %w", path, err)
	}
	return createMachineKey(dataDir, path, false)
}

// createMachineKey installs a fresh seed atomically: a temp file in the
// same directory is written, fsynced and linked into place (link never
// replaces an existing key, so a concurrent create still loses cleanly),
// then the directory is fsynced. A crash before the link leaves only an
// ignored temp file — never a half-written machine-key. replaceEmpty is
// the interrupted-first-create recovery: the caller holds the data-dir
// flock, and an empty file carried no identity.
func createMachineKey(dataDir, path string, replaceEmpty bool) (ed25519.PrivateKey, string, error) {
	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		return nil, "", fmt.Errorf("machine key: rand: %w", err)
	}
	tmp, err := os.CreateTemp(dataDir, "."+machineKeyFile+".tmp-*")
	if err != nil {
		return nil, "", fmt.Errorf("create machine key temp in %s: %w", dataDir, err)
	}
	tmpName := tmp.Name()
	// Remove the temp name whether or not the link below installed it:
	// after a successful link the seed lives at path under its own name.
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return nil, "", fmt.Errorf("chmod machine key temp %s: %w", tmpName, err)
	}
	if _, err := tmp.Write([]byte(hex.EncodeToString(seed) + "\n")); err != nil {
		tmp.Close()
		return nil, "", fmt.Errorf("write machine key temp %s: %w", tmpName, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return nil, "", fmt.Errorf("sync machine key temp %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return nil, "", err
	}
	if replaceEmpty {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return nil, "", fmt.Errorf("remove empty machine key %s: %w", path, err)
		}
	}
	if err := os.Link(tmpName, path); err != nil {
		if os.IsExist(err) {
			// Another start won the create race: read its key.
			raw, rerr := os.ReadFile(path)
			if rerr != nil {
				return nil, "", rerr
			}
			return parseMachineKey(path, raw)
		}
		return nil, "", fmt.Errorf("install machine key %s: %w", path, err)
	}
	if err := syncDir(dataDir); err != nil {
		return nil, "", fmt.Errorf("sync data dir %s: %w", dataDir, err)
	}
	return privateFromSeed(seed)
}

// syncDir makes the key's directory entry durable.
func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func parseMachineKey(path string, raw []byte) (ed25519.PrivateKey, string, error) {
	text := strings.TrimSpace(string(raw))
	seed, err := hex.DecodeString(text)
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, "", fmt.Errorf("machine key %s is corrupt (want a %d-byte hex seed): fix or remove it deliberately — the daemon never regenerates an identity silently", path, ed25519.SeedSize)
	}
	return privateFromSeed(seed)
}

func privateFromSeed(seed []byte) (ed25519.PrivateKey, string, error) {
	key := ed25519.NewKeyFromSeed(seed)
	pub := key.Public().(ed25519.PublicKey)
	return key, base64.StdEncoding.EncodeToString(pub), nil
}
