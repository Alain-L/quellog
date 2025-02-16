// analysis/utils.go
package analysis

import (
	"crypto/md5"
	"encoding/base64"
	"fmt"
	"strings"
	"time"
)

// truncateQuery tronque la chaîne query à la largeur spécifiée, ajoutant "..." si nécessaire.
func truncateQuery(query string, length int) string {
	if len(query) > length {
		return query[:length-3] + "..."
	}
	return query
}

// Normalisation de la requête SQL (remplace valeurs dynamiques)
func normalizeQuery(query string) string {
	query = strings.ReplaceAll(query, "\n", " ") // Unifier sur une ligne
	query = strings.ToLower(query)               // Uniformisation
	query = strings.ReplaceAll(query, "$", "?")  // Remplacer les variables PostgreSQL
	return query
}

func formatQueryDuration(ms float64) string {
	// Convertir la durée en time.Duration
	d := time.Duration(ms * float64(time.Millisecond))

	// Si moins d'une seconde, afficher en millisecondes
	if d < time.Second {
		return fmt.Sprintf("%d ms", d/time.Millisecond)
	}

	// Si moins d'une minute, afficher en secondes avec 2 décimales
	if d < time.Minute {
		return fmt.Sprintf("%.2f s", d.Seconds())
	}

	// Si moins d'une heure, afficher en minutes et secondes
	if d < time.Hour {
		minutes := int(d / time.Minute)
		seconds := int((d % time.Minute) / time.Second)
		return fmt.Sprintf("%dm %02ds", minutes, seconds)
	}

	// Si moins de 24 heures, afficher en heures, minutes, secondes
	if d < 24*time.Hour {
		hours := int(d / time.Hour)
		minutes := int((d % time.Hour) / time.Minute)
		seconds := int((d % time.Minute) / time.Second)
		return fmt.Sprintf("%dh %02dm %02ds", hours, minutes, seconds)
	}

	// Sinon, afficher en jours, heures, minutes
	days := int(d / (24 * time.Hour))
	hours := int((d % (24 * time.Hour)) / time.Hour)
	minutes := int((d % time.Hour) / time.Minute)
	return fmt.Sprintf("%dd %dh %02dm", days, hours, minutes)
}

// generateQueryID génère un identifiant court à partir de la requête brute et normalisée.
func generateQueryID(rawQuery, normalizedQuery string) (id, fullHash string) {
	// Déterminer le préfixe en fonction du type de requête.
	lower := strings.ToLower(strings.TrimSpace(rawQuery))
	prefix := "xx-" // valeur par défaut
	if strings.HasPrefix(lower, "select") {
		prefix = "se-"
	} else if strings.HasPrefix(lower, "insert") {
		prefix = "in-"
	} else if strings.HasPrefix(lower, "update") {
		prefix = "up-"
	} else if strings.HasPrefix(lower, "delete") {
		prefix = "de-"
	} else if strings.HasPrefix(lower, "copy") {
		prefix = "co-"
	} else if strings.HasPrefix(lower, "refresh") {
		prefix = "mv-"
	}

	// Calculer le hash MD5 complet de la requête normalisée.
	hashBytes := md5.Sum([]byte(normalizedQuery))
	fullHash = strings.ToLower(fmt.Sprintf("%x", hashBytes)) // 32 hex chars

	// Convertir le hash en base64 pour obtenir une version plus compacte.
	b64 := base64.StdEncoding.EncodeToString(hashBytes[:])
	// Remplacer les caractères spéciaux pour obtenir une chaîne alphanumérique.
	b64 = strings.NewReplacer("+", "", "/", "", "=", "").Replace(b64)
	// Extraire, par exemple, les 6 premiers caractères.
	shortHash := b64
	if len(b64) > 6 {
		shortHash = b64[:6]
	}

	// Assembler l'ID.
	id = prefix + shortHash
	return
}

func formatAverageLoad(load float64) string {
	// load est en queries/s
	if load >= 1.0 {
		return fmt.Sprintf("%.2f queries/s", load)
	}
	perMin := load * 60.0
	if perMin >= 1.0 {
		// Afficher en queries/min sans décimales
		return fmt.Sprintf("%.0f queries/min", perMin)
	}
	perHour := load * 3600.0
	return fmt.Sprintf("%.0f queries/h", perHour)
}
