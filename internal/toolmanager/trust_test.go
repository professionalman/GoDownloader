package toolmanager

import (
	"bytes"
	"crypto"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
)

// Real upstream fixtures captured from the official stable release 2026.10.26 via the GitHub release asset API.
const (
	upstreamFixtureDir    = "testdata/upstream-2026.10.26"
	upstreamFixtureTag    = "2026.10.26"
	upstreamFixtureExeSHA = "f5389d3698ecde4ef06f4e6b1ce0294fe9ac1396228c8990b8d3416d6d177a7d"
	historicalYoutubeDLFP = "ED7F5BF46B3BBED81C87368E2C393E0F18A9236D"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(upstreamFixtureDir, name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}

func TestTrust_EmbeddedKeyParsesWithPinnedFingerprint(t *testing.T) {
	root, err := ytdlpTrustRoot()
	if err != nil {
		t.Fatalf("embedded key rejected: %v", err)
	}
	if root.fingerprint != YtdlpSigningKeyFingerprint {
		t.Fatalf("fingerprint %s, want %s", root.fingerprint, YtdlpSigningKeyFingerprint)
	}
	if root.fingerprint == historicalYoutubeDLFP {
		t.Fatal("historical youtube-dl key must not be trusted")
	}
	identities := ""
	for name := range root.keyring[0].Identities {
		identities += name + ";"
	}
	if !strings.Contains(identities, "Simon Sawicki") {
		t.Fatalf("unexpected key identity: %q", identities)
	}
}

func TestTrust_EmbeddedKeyWithWrongPinFailsClosed(t *testing.T) {
	if _, err := newTrustRoot(ytdlpSigningKeyArmored, historicalYoutubeDLFP); !errors.Is(err, ErrUntrustedSigningKey) {
		t.Fatalf("expected ErrUntrustedSigningKey for mismatched pin, got %v", err)
	}
	if _, err := newTrustRoot([]byte("not a key"), YtdlpSigningKeyFingerprint); !errors.Is(err, ErrUntrustedSigningKey) {
		t.Fatalf("expected ErrUntrustedSigningKey for garbage key, got %v", err)
	}
}

// Real official signature verified through the production verifier.
func TestTrust_RealUpstreamSignatureVerifies(t *testing.T) {
	root, err := ytdlpTrustRoot()
	if err != nil {
		t.Fatal(err)
	}
	manifest := readFixture(t, "SHA2-256SUMS")
	sig := readFixture(t, "SHA2-256SUMS.sig")
	if err := root.verifyDetachedSignature(manifest, sig); err != nil {
		t.Fatalf("official %s signature failed verification: %v", upstreamFixtureTag, err)
	}
	digest, err := parseSHA256Manifest(manifest, ytdlpWindowsAsset)
	if err != nil {
		t.Fatalf("parse authenticated manifest: %v", err)
	}
	if digest != upstreamFixtureExeSHA {
		t.Fatalf("authenticated yt-dlp.exe digest %s, want %s", digest, upstreamFixtureExeSHA)
	}
}

func TestTrust_TamperedManifestRejected(t *testing.T) {
	root, _ := ytdlpTrustRoot()
	manifest := readFixture(t, "SHA2-256SUMS")
	sig := readFixture(t, "SHA2-256SUMS.sig")
	tampered := bytes.Replace(manifest, []byte(upstreamFixtureExeSHA), []byte(strings.Repeat("0", 64)), 1)
	if err := root.verifyDetachedSignature(tampered, sig); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("expected ErrInvalidSignature for tampered manifest, got %v", err)
	}
}

func TestTrust_CorruptAndOversizedSignatureRejected(t *testing.T) {
	root, _ := ytdlpTrustRoot()
	manifest := readFixture(t, "SHA2-256SUMS")
	sig := readFixture(t, "SHA2-256SUMS.sig")
	corrupt := append([]byte(nil), sig...)
	corrupt[len(corrupt)-10] ^= 0xFF
	if err := root.verifyDetachedSignature(manifest, corrupt); err == nil {
		t.Fatal("corrupt signature accepted")
	}
	if err := root.verifyDetachedSignature(manifest, make([]byte, maxSignatureBytes+1)); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("oversized signature: %v", err)
	}
	if err := root.verifyDetachedSignature(make([]byte, maxManifestBytes+1), sig); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("oversized manifest: %v", err)
	}
	if err := root.verifyDetachedSignature(manifest, nil); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("empty signature: %v", err)
	}
}

// syntheticSigner creates a throwaway signing entity for offline tests.
func syntheticSigner(t *testing.T) (*openpgp.Entity, []byte) {
	return syntheticSignerWithHash(t, crypto.SHA256)
}

// syntheticSignerWithHash creates a signing entity whose preferred (and therefore actual) signature hash is
// hash; go-crypto follows the key's preference list rather than a per-call override.
func syntheticSignerWithHash(t *testing.T, hash crypto.Hash) (*openpgp.Entity, []byte) {
	t.Helper()
	cfg := &packet.Config{Algorithm: packet.PubKeyAlgoEdDSA, DefaultHash: hash}
	entity, err := openpgp.NewEntity("Synthetic Test Signer", "", "test@example.invalid", cfg)
	if err != nil {
		t.Fatalf("NewEntity: %v", err)
	}
	var armored bytes.Buffer
	w, err := armor.Encode(&armored, openpgp.PublicKeyType, nil)
	if err != nil {
		t.Fatalf("armor key: %v", err)
	}
	if err := entity.Serialize(w); err != nil {
		t.Fatalf("serialize key: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close armor: %v", err)
	}
	return entity, armored.Bytes()
}

func signDetached(t *testing.T, signer *openpgp.Entity, data []byte, hash crypto.Hash) []byte {
	t.Helper()
	var sig bytes.Buffer
	if err := openpgp.DetachSign(&sig, signer, bytes.NewReader(data), &packet.Config{DefaultHash: hash}); err != nil {
		t.Fatalf("DetachSign: %v", err)
	}
	return sig.Bytes()
}

func TestTrust_SyntheticSignatureVerifiesOffline(t *testing.T) {
	signer, armored := syntheticSigner(t)
	fp := strings.ToUpper(hex.EncodeToString(signer.PrimaryKey.Fingerprint))
	root, err := newTrustRoot(armored, fp)
	if err != nil {
		t.Fatalf("synthetic trust root: %v", err)
	}
	manifest := []byte(strings.Repeat("a", 64) + "  yt-dlp.exe\n")
	if err := root.verifyDetachedSignature(manifest, signDetached(t, signer, manifest, crypto.SHA256)); err != nil {
		t.Fatalf("synthetic signature rejected: %v", err)
	}
}

func TestTrust_WrongSignerRejected(t *testing.T) {
	// Official trust root, signature from an unrelated key.
	root, _ := ytdlpTrustRoot()
	impostor, _ := syntheticSigner(t)
	manifest := readFixture(t, "SHA2-256SUMS")
	err := root.verifyDetachedSignature(manifest, signDetached(t, impostor, manifest, crypto.SHA256))
	if !errors.Is(err, ErrUntrustedSigningKey) {
		t.Fatalf("expected ErrUntrustedSigningKey for impostor signature, got %v", err)
	}
}

// go-crypto will not produce weak-digest signatures with modern keys, so the post-verification algorithm
// policy is exercised directly. go-crypto's own RejectHashAlgorithms (verifierConfig) is a second layer.
func TestTrust_SignatureAlgorithmPolicy(t *testing.T) {
	cases := []struct {
		name string
		sig  *packet.Signature
		ok   bool
	}{
		{"rsa sha256 (official)", &packet.Signature{Hash: crypto.SHA256, PubKeyAlgo: packet.PubKeyAlgoRSA}, true},
		{"rsa sha512", &packet.Signature{Hash: crypto.SHA512, PubKeyAlgo: packet.PubKeyAlgoRSA}, true},
		{"eddsa sha256", &packet.Signature{Hash: crypto.SHA256, PubKeyAlgo: packet.PubKeyAlgoEdDSA}, true},
		{"sha1", &packet.Signature{Hash: crypto.SHA1, PubKeyAlgo: packet.PubKeyAlgoRSA}, false},
		{"md5", &packet.Signature{Hash: crypto.MD5, PubKeyAlgo: packet.PubKeyAlgoRSA}, false},
		{"sha224", &packet.Signature{Hash: crypto.SHA224, PubKeyAlgo: packet.PubKeyAlgoRSA}, false},
		{"dsa", &packet.Signature{Hash: crypto.SHA256, PubKeyAlgo: packet.PubKeyAlgoDSA}, false},
		{"ecdsa", &packet.Signature{Hash: crypto.SHA256, PubKeyAlgo: packet.PubKeyAlgoECDSA}, false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		err := checkSignatureAlgorithms(tc.sig)
		if tc.ok && err != nil {
			t.Errorf("%s: unexpectedly rejected: %v", tc.name, err)
		}
		if !tc.ok && !errors.Is(err, ErrInvalidSignature) {
			t.Errorf("%s: expected ErrInvalidSignature, got %v", tc.name, err)
		}
	}
	for _, weak := range []crypto.Hash{crypto.MD5, crypto.SHA1, crypto.RIPEMD160, crypto.SHA224} {
		if !verifierConfig.RejectHashAlgorithm(weak) {
			t.Errorf("verifier config does not reject %v", weak)
		}
	}
	if verifierConfig.MinimumRSABits() < 3072 {
		t.Errorf("minimum RSA bits too low: %d", verifierConfig.MinimumRSABits())
	}
}

func TestTrust_OfficialSignatureUsesAllowedAlgorithms(t *testing.T) {
	p, err := packet.Read(bytes.NewReader(readFixture(t, "SHA2-256SUMS.sig")))
	if err != nil {
		t.Fatal(err)
	}
	sig, ok := p.(*packet.Signature)
	if !ok {
		t.Fatalf("fixture is not a signature packet: %T", p)
	}
	if err := checkSignatureAlgorithms(sig); err != nil {
		t.Fatalf("official signature algorithms rejected: %v", err)
	}
	t.Logf("official signature: hash=%v pubkey=%v", sig.Hash, sig.PubKeyAlgo)
}

func TestTrust_ArmoredSignatureRejected(t *testing.T) {
	root, _ := ytdlpTrustRoot()
	manifest := readFixture(t, "SHA2-256SUMS")
	armored := []byte("-----BEGIN PGP SIGNATURE-----\n\nAAAA\n-----END PGP SIGNATURE-----\n")
	if err := root.verifyDetachedSignature(manifest, armored); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("expected ErrInvalidSignature for armored signature, got %v", err)
	}
}
