// analysis/utils.go

package analysis

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"sync"
)

// Pool de strings.Builder pour réduire les allocations
var builderPool = sync.Pool{
	New: func() interface{} {
		return new(strings.Builder)
	},
}

// Lookup table pré-calculée pour éviter les maps
var queryPrefixes = [...]struct {
	keyword string
	prefix  string
}{
	{"SELECT", "se-"},
	{"INSERT", "in-"},
	{"UPDATE", "up-"},
	{"DELETE", "de-"},
	{"COPY", "co-"},
	{"REFRESH", "mv-"},
}

// normalizeQuery standardizes the SQL query by replacing dynamic values.
// OPTIMISÉ: Une seule passe sur la string, pas de ReplaceAll multiples
func normalizeQuery(query string) string {
	if len(query) == 0 {
		return ""
	}

	buf := builderPool.Get().(*strings.Builder)
	buf.Reset()
	buf.Grow(len(query)) // Pré-alloue la taille exacte
	defer builderPool.Put(buf)

	for i := 0; i < len(query); i++ {
		c := query[i]
		switch c {
		case '\n', '\r', '\t':
			buf.WriteByte(' ') // Remplace les whitespaces par espace
		case '$':
			buf.WriteByte('?') // Remplace $ par ?
		default:
			// Conversion lowercase inline (plus rapide que ToLower)
			if c >= 'A' && c <= 'Z' {
				buf.WriteByte(c + 32)
			} else {
				buf.WriteByte(c)
			}
		}
	}

	return buf.String()
}

// GenerateQueryID generates a short identifier from the raw and normalized query.
// It determines a prefix based on the query type and computes an MD5 hash from the normalized query.
func GenerateQueryID(rawQuery, normalizedQuery string) (id, fullHash string) {
	// Préfixes SQL typiques (lookup array)
	prefix := "xx-" // Default
	for _, p := range queryPrefixes {
		// Compare directement en ignorant la casse
		if len(rawQuery) >= len(p.keyword) {
			match := true
			for j := 0; j < len(p.keyword); j++ {
				c := rawQuery[j]
				if c >= 'a' && c <= 'z' {
					c -= 32 // Convert to uppercase
				}
				if c != p.keyword[j] {
					match = false
					break
				}
			}
			if match {
				prefix = p.prefix
				break
			}
		}
	}

	// Compute the MD5 hash of the normalized query.
	hashBytes := md5.Sum([]byte(normalizedQuery))
	fullHash = hex.EncodeToString(hashBytes[:]) // 32 hex characters.

	// ✅ OPTIMISÉ: Base64 URL-safe sans padding (plus compact, pas de caractères à nettoyer)
	// Prend les 5 premiers bytes du hash (40 bits) → 7 chars base64 → tronque à 6
	b64 := base64.RawURLEncoding.EncodeToString(hashBytes[:5])

	// Les 6 premiers caractères donnent ~36 bits d'entropie
	shortHash := b64[:6]

	id = prefix + shortHash
	return
}

// QueryTypeFromID returns the query type based on the identifier prefix.
func QueryTypeFromID(id string) string {
	if len(id) < 3 {
		return "other"
	}

	// Lookup direct des 3 premiers caractères
	switch id[:3] {
	case "se-":
		return "select"
	case "in-":
		return "insert"
	case "up-":
		return "update"
	case "de-":
		return "delete"
	case "co-":
		return "copy"
	case "mv-":
		return "refresh"
	default:
		return "other"
	}
}
