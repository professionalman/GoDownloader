package toolmanager

import (
	"bytes"
	"crypto"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/ProtonMail/go-crypto/openpgp"
	pgperrors "github.com/ProtonMail/go-crypto/openpgp/errors"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
)

// YtdlpSigningKeyFingerprint is the full OpenPGP fingerprint of the official yt-dlp release signing key
// (Simon Sawicki, yt-dlp signing key). It is pinned in source; there is no trust-on-first-use and no
// automatic key rotation. The historical youtube-dl key (ED7F5BF4...F18A9236D) is deliberately not trusted.
const YtdlpSigningKeyFingerprint = "AC0CBBE6848D6A873464AF4E57CF65933B5A7581"

// ytdlpSigningKeyArmored is the reviewed upstream public key from
// https://github.com/yt-dlp/yt-dlp/blob/master/public.key, embedded so runtime never fetches a key.
//
//go:embed ytdlp_signing_key.asc
var ytdlpSigningKeyArmored []byte

const (
	maxSignatureBytes = 64 << 10
	maxManifestBytes  = 64 << 10
)

// signatureHashAllowlist lists the only digest algorithms accepted on release signatures.
var signatureHashAllowlist = map[crypto.Hash]bool{
	crypto.SHA256: true,
	crypto.SHA384: true,
	crypto.SHA512: true,
}

// signaturePubKeyAllowlist lists the only public-key algorithms accepted on release signatures.
var signaturePubKeyAllowlist = map[packet.PublicKeyAlgorithm]bool{
	packet.PubKeyAlgoRSA:     true,
	packet.PubKeyAlgoEdDSA:   true,
	packet.PubKeyAlgoEd25519: true,
	packet.PubKeyAlgoEd448:   true,
}

// verifierConfig hardens go-crypto defaults: weak digests are rejected and RSA keys must be >= 3072 bits.
var verifierConfig = &packet.Config{
	MinRSABits: 3072,
	RejectHashAlgorithms: map[crypto.Hash]bool{
		crypto.MD5:       true,
		crypto.SHA1:      true,
		crypto.RIPEMD160: true,
		crypto.SHA224:    true,
	},
}

// trustRoot is a keyring containing exactly one entity whose primary fingerprint is pinned.
type trustRoot struct {
	keyring     openpgp.EntityList
	fingerprint string
}

var (
	ytdlpTrustOnce sync.Once
	ytdlpTrust     *trustRoot
	ytdlpTrustErr  error
)

// ytdlpTrustRoot parses the embedded key once and fails closed if its identity differs from the pin.
func ytdlpTrustRoot() (*trustRoot, error) {
	ytdlpTrustOnce.Do(func() {
		ytdlpTrust, ytdlpTrustErr = newTrustRoot(ytdlpSigningKeyArmored, YtdlpSigningKeyFingerprint)
	})
	return ytdlpTrust, ytdlpTrustErr
}

func newTrustRoot(armoredKey []byte, expectedFingerprint string) (*trustRoot, error) {
	keyring, err := openpgp.ReadArmoredKeyRing(bytes.NewReader(armoredKey))
	if err != nil {
		return nil, fmt.Errorf("%w: parse embedded signing key: %v", ErrUntrustedSigningKey, err)
	}
	if len(keyring) != 1 || keyring[0].PrimaryKey == nil {
		return nil, fmt.Errorf("%w: embedded key material must contain exactly one key", ErrUntrustedSigningKey)
	}
	got := strings.ToUpper(hex.EncodeToString(keyring[0].PrimaryKey.Fingerprint))
	if got != strings.ToUpper(expectedFingerprint) {
		return nil, fmt.Errorf("%w: embedded key fingerprint does not match the pinned fingerprint", ErrUntrustedSigningKey)
	}
	return &trustRoot{keyring: keyring, fingerprint: got}, nil
}

// verifyDetachedSignature authenticates manifest against a binary detached OpenPGP signature made by the
// pinned key. Any parse failure, unknown signer, weak algorithm, or digest mismatch fails closed.
func (t *trustRoot) verifyDetachedSignature(manifest, signature []byte) error {
	if len(manifest) == 0 || len(manifest) > maxManifestBytes {
		return fmt.Errorf("%w: manifest size %d outside accepted bounds", ErrInvalidSignature, len(manifest))
	}
	if len(signature) == 0 || len(signature) > maxSignatureBytes {
		return fmt.Errorf("%w: signature size %d outside accepted bounds", ErrInvalidSignature, len(signature))
	}
	if bytes.HasPrefix(bytes.TrimSpace(signature), []byte("-----BEGIN")) {
		return fmt.Errorf("%w: armored signatures are not accepted for this release channel", ErrInvalidSignature)
	}

	sig, signer, err := openpgp.VerifyDetachedSignature(t.keyring, bytes.NewReader(manifest), bytes.NewReader(signature), verifierConfig)
	if err != nil {
		if errors.Is(err, pgperrors.ErrUnknownIssuer) {
			return fmt.Errorf("%w: signature was not made by the pinned yt-dlp signing key", ErrUntrustedSigningKey)
		}
		return fmt.Errorf("%w: %v", ErrInvalidSignature, err)
	}
	if signer == nil || signer.PrimaryKey == nil ||
		strings.ToUpper(hex.EncodeToString(signer.PrimaryKey.Fingerprint)) != t.fingerprint {
		return fmt.Errorf("%w: signer fingerprint mismatch", ErrUntrustedSigningKey)
	}
	return checkSignatureAlgorithms(sig)
}

// checkSignatureAlgorithms enforces the digest and public-key algorithm allowlists on a verified signature.
func checkSignatureAlgorithms(sig *packet.Signature) error {
	if sig == nil || !signatureHashAllowlist[sig.Hash] {
		return fmt.Errorf("%w: unsupported signature digest algorithm", ErrInvalidSignature)
	}
	if !signaturePubKeyAllowlist[sig.PubKeyAlgo] {
		return fmt.Errorf("%w: unsupported signature public-key algorithm", ErrInvalidSignature)
	}
	return nil
}
