package job

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/anacrolix/torrent/bencode"
)

// TorrentHashIdentity represents the multi-version torrent identity.
type TorrentHashIdentity struct {
	V1Hash        string // optional 40-character lowercase hex (SHA-1)
	V2Hash        string // optional 64-character lowercase hex (SHA-256)
	QBitTorrentID string // always 40-character lowercase hex (qBittorrent / libtorrent get_best identifier)
}

type bencodeMetaInfo struct {
	Info bencode.Bytes `bencode:"info"`
}

// ExtractTorrentIdentity parses raw .torrent file bytes and returns the TorrentHashIdentity.
// Follows libtorrent / qBittorrent get_best semantics:
//   - v1 only: SHA1(raw info dictionary) (40 hex chars)
//   - v2 only: first 20 bytes of SHA256(raw info dictionary) (40 hex chars)
//   - hybrid: first 20 bytes of SHA256(raw info dictionary) (40 hex chars)
func ExtractTorrentIdentity(data []byte) (TorrentHashIdentity, error) {
	if len(data) == 0 {
		return TorrentHashIdentity{}, errors.New("empty torrent data")
	}

	var meta bencodeMetaInfo
	if err := bencode.Unmarshal(data, &meta); err != nil {
		return TorrentHashIdentity{}, fmt.Errorf("invalid bencoded torrent file: %w", err)
	}

	if len(meta.Info) == 0 {
		return TorrentHashIdentity{}, errors.New("missing info dictionary in torrent file")
	}

	var infoDict map[string]interface{}
	if err := bencode.Unmarshal(meta.Info, &infoDict); err != nil {
		return TorrentHashIdentity{}, fmt.Errorf("invalid info dictionary in torrent file: %w", err)
	}

	var ident TorrentHashIdentity

	// Check for BitTorrent v1 indicators: "pieces" key in info dictionary
	_, hasPieces := infoDict["pieces"]
	if hasPieces {
		h1 := sha1.Sum(meta.Info)
		ident.V1Hash = strings.ToLower(hex.EncodeToString(h1[:]))
	}

	// Check for BitTorrent v2 indicators: "meta version" == 2 or "file tree"
	metaVer, hasMetaVer := infoDict["meta version"]
	_, hasFileTree := infoDict["file tree"]
	isV2 := hasFileTree
	if hasMetaVer {
		if verInt, ok := metaVer.(int64); ok && verInt == 2 {
			isV2 = true
		}
	}

	if isV2 {
		h2 := sha256.Sum256(meta.Info)
		ident.V2Hash = strings.ToLower(hex.EncodeToString(h2[:]))
	}

	// Derive QBitTorrentID according to libtorrent / qBittorrent get_best semantics:
	if ident.V2Hash != "" {
		// v2 only or hybrid: first 20 bytes (40 hex characters) of SHA-256
		ident.QBitTorrentID = ident.V2Hash[:40]
	} else if ident.V1Hash != "" {
		// v1 only: SHA-1 hash (40 hex characters)
		ident.QBitTorrentID = ident.V1Hash
	} else {
		// Fallback for custom metainfo: SHA-1 of info dictionary
		h1 := sha1.Sum(meta.Info)
		ident.V1Hash = strings.ToLower(hex.EncodeToString(h1[:]))
		ident.QBitTorrentID = ident.V1Hash
	}

	return ident, nil
}

// ExtractTorrentIdentityFromFile reads a .torrent file from disk and returns its TorrentHashIdentity.
func ExtractTorrentIdentityFromFile(filePath string) (TorrentHashIdentity, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return TorrentHashIdentity{}, fmt.Errorf("read torrent file %s: %w", filePath, err)
	}
	return ExtractTorrentIdentity(data)
}

// ExtractTorrentInfoHash computes the canonical 40-character lowercase hex QBitTorrentID from raw .torrent file bytes.
func ExtractTorrentInfoHash(data []byte) (string, error) {
	ident, err := ExtractTorrentIdentity(data)
	if err != nil {
		return "", err
	}
	return ident.QBitTorrentID, nil
}

// ExtractTorrentInfoHashFromFile reads a .torrent file from disk and computes its QBitTorrentID.
func ExtractTorrentInfoHashFromFile(filePath string) (string, error) {
	ident, err := ExtractTorrentIdentityFromFile(filePath)
	if err != nil {
		return "", err
	}
	return ident.QBitTorrentID, nil
}

// ExtractMagnetIdentity parses a magnet URI and extracts its v1/v2 identity and qBittorrent TorrentID.
// Supports BEP 9 and BEP 52/53 v2 multihash (xt=urn:btmh:1220<64-hex>) and hybrid magnets.
func ExtractMagnetIdentity(magnet string) (TorrentHashIdentity, error) {
	lower := strings.ToLower(magnet)
	if !strings.HasPrefix(lower, "magnet:?") && !strings.HasPrefix(lower, "magnet:") {
		return TorrentHashIdentity{}, fmt.Errorf("invalid magnet link: missing magnet prefix")
	}

	var ident TorrentHashIdentity

	rawQuery := magnet
	if qIdx := strings.Index(magnet, "?"); qIdx != -1 {
		rawQuery = magnet[qIdx+1:]
	}

	params := strings.Split(rawQuery, "&")
	for _, param := range params {
		p := strings.TrimSpace(param)
		if strings.HasPrefix(strings.ToLower(p), "xt=") {
			val := p[3:]
			if unescaped, err := url.QueryUnescape(val); err == nil {
				val = unescaped
			}
			lowerVal := strings.ToLower(val)

			if strings.HasPrefix(lowerVal, "urn:btih:") {
				hashPart := val[len("urn:btih:"):]
				if amp := strings.Index(hashPart, "&"); amp != -1 {
					hashPart = hashPart[:amp]
				}
				hashPart = strings.TrimSpace(hashPart)
				if len(hashPart) == 40 && isHex(hashPart) {
					ident.V1Hash = strings.ToLower(hashPart)
				} else if len(hashPart) == 32 {
					upperHash := strings.ToUpper(hashPart)
					if decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(upperHash); err == nil && len(decoded) == 20 {
						ident.V1Hash = strings.ToLower(hex.EncodeToString(decoded))
					}
				}
			} else if strings.HasPrefix(lowerVal, "urn:btmh:") {
				hashPart := val[len("urn:btmh:"):]
				if amp := strings.Index(hashPart, "&"); amp != -1 {
					hashPart = hashPart[:amp]
				}
				hashPart = strings.TrimSpace(hashPart)
				// BEP 52: 1220 prefix indicates sha256 multihash (0x12=sha256, 0x20=32 bytes) followed by 64 hex chars
				if strings.HasPrefix(strings.ToLower(hashPart), "1220") && len(hashPart) == 68 {
					v2Hex := hashPart[4:]
					if isHex(v2Hex) {
						ident.V2Hash = strings.ToLower(v2Hex)
					}
				} else if len(hashPart) == 64 && isHex(hashPart) {
					ident.V2Hash = strings.ToLower(hashPart)
				}
			}
		}
	}

	// Derive QBitTorrentID according to libtorrent / qBittorrent get_best semantics
	if ident.V2Hash != "" {
		ident.QBitTorrentID = ident.V2Hash[:40]
	} else if ident.V1Hash != "" {
		ident.QBitTorrentID = ident.V1Hash
	} else {
		return TorrentHashIdentity{}, fmt.Errorf("invalid magnet link: no valid btih or btmh info hash found")
	}

	return ident, nil
}

func isHex(s string) bool {
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// ExtractMagnetHash extracts and normalizes the 40-character lowercase hex QBitTorrentID from a magnet URI string.
func ExtractMagnetHash(magnet string) (string, error) {
	ident, err := ExtractMagnetIdentity(magnet)
	if err != nil {
		return "", err
	}
	return ident.QBitTorrentID, nil
}
