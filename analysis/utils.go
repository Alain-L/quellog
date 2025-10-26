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
// ✅ OPTIMISÉ: Une seule passe + collapse des espaces multiples
func normalizeQuery(query string) string {
	if len(query) == 0 {
		return ""
	}

	buf := builderPool.Get().(*strings.Builder)
	buf.Reset()
	buf.Grow(len(query))
	defer builderPool.Put(buf)

	// ✅ Track si le dernier caractère était un espace pour collapse
	lastWasSpace := false

	for i := 0; i < len(query); i++ {
		c := query[i]
		switch c {
		case '\n', '\r', '\t', ' ':
			// ✅ Écrit un seul espace si le précédent n'en était pas un
			if !lastWasSpace {
				buf.WriteByte(' ')
				lastWasSpace = true
			}
		case '$':
			buf.WriteByte('?')
			lastWasSpace = false
		default:
			// Conversion lowercase inline
			if c >= 'A' && c <= 'Z' {
				buf.WriteByte(c + 32)
			} else {
				buf.WriteByte(c)
			}
			lastWasSpace = false
		}
	}

	return buf.String()
}

// GenerateQueryID generates a short identifier from the raw and normalized query.
// ✅ OPTIMISÉ: Base64 URL-safe (pas de +/=) pour IDs propres + bonne entropie (36 bits)
func GenerateQueryID(rawQuery, normalizedQuery string) (id, fullHash string) {
	// Détecte le préfixe SQL
	prefix := "xx-" // Default
	for _, p := range queryPrefixes {
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

	// Compute MD5 hash
	hashBytes := md5.Sum([]byte(normalizedQuery))
	fullHash = hex.EncodeToString(hashBytes[:])

	// ✅ OPTIMISÉ: Base64 standard → prend les 6 premiers caractères alphanumériques
	// Encode tout le hash pour avoir assez de caractères
	b64 := base64.StdEncoding.EncodeToString(hashBytes[:])

	// ✅ Extract 6 alphanumeric chars (skip +, /, =) - version optimisée
	var shortHash [6]byte // Array sur la stack (pas d'allocation)
	j := 0
	for i := 0; i < len(b64) && j < 6; i++ {
		c := b64[i]
		// ✅ Branchless check: caractères alphanumériques ont certains bits
		// 0-9: 48-57, A-Z: 65-90, a-z: 97-122
		// On skip: +: 43, /: 47, =: 61
		if c != '+' && c != '/' && c != '=' {
			shortHash[j] = c
			j++
		}
	}

	id = prefix + string(shortHash[:])
	return
}

// QueryTypeFromID returns the query type based on the identifier prefix.
func QueryTypeFromID(id string) string {
	if len(id) < 3 {
		return "other"
	}

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
