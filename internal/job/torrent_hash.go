package job

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"

	"github.com/anacrolix/torrent/bencode"
)

type bencodeMetaInfo struct {
	Info bencode.Bytes `bencode:"info"`
}

// ExtractTorrentInfoHash computes the canonical lowercase hex info hash from raw .torrent file bytes.
// Supports standard BitTorrent v1 (SHA-1), BitTorrent v2 (SHA-256), and hybrid torrents (v1 SHA-1).
func ExtractTorrentInfoHash(data []byte) (string, error) {
	if len(data) == 0 {
		return "", errors.New("empty torrent data")
	}

	var meta bencodeMetaInfo
	if err := bencode.Unmarshal(data, &meta); err != nil {
		return "", fmt.Errorf("invalid bencoded torrent file: %w", err)
	}

	if len(meta.Info) == 0 {
		return "", errors.New("missing info dictionary in torrent file")
	}

	// Check if this is a pure BitTorrent v2 torrent without v1 pieces
	var infoDict map[string]interface{}
	if err := bencode.Unmarshal(meta.Info, &infoDict); err == nil {
		_, hasPieces := infoDict["pieces"]
		metaVer, hasMetaVer := infoDict["meta version"]
		if !hasPieces && hasMetaVer {
			if verInt, ok := metaVer.(int64); ok && verInt == 2 {
				h2 := sha256.Sum256(meta.Info)
				return hex.EncodeToString(h2[:]), nil
			}
		}
	}

	// Standard v1 or hybrid torrent info hash (SHA-1 of the bencoded info dictionary)
	h1 := sha1.Sum(meta.Info)
	return hex.EncodeToString(h1[:]), nil
}

// ExtractTorrentInfoHashFromFile reads a .torrent file from disk and computes its info hash.
func ExtractTorrentInfoHashFromFile(filePath string) (string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("read torrent file %s: %w", filePath, err)
	}
	return ExtractTorrentInfoHash(data)
}
