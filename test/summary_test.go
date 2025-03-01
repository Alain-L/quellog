// analysis/summary_test.go
package quellog_test

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// 📌 Teste si Quellog génère un summary cohérent
func TestSummaryReport(t *testing.T) {
	// Définition du chemin vers le binaire (assure-toi qu’il est bien buildé)
	quellogBinary := "../bin/quellog" // Ajuste si besoin

	// Définition du fichier de test
	logFile := "testdata/test_01.log"      // Un log de test
	expectedFile := "testdata/test_01.out" // Résultat attendu

	// Exécuter quellog avec le fichier de test
	cmd := exec.Command(quellogBinary, logFile, "-S")
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = os.Stderr

	err := cmd.Run()
	if err != nil {
		t.Fatalf("Échec de l'exécution de quellog: %v", err)
	}

	// Lire le résultat attendu
	expectedOutput, err := os.ReadFile(expectedFile)
	if err != nil {
		t.Fatalf("Impossible de lire le fichier attendu: %v", err)
	}

	// Comparer les sorties
	if strings.TrimSpace(output.String()) != strings.TrimSpace(string(expectedOutput)) {
		t.Errorf("Les sorties ne correspondent pas !\n--- Attendu ---\n%s\n--- Obtenu ---\n%s\n",
			expectedOutput, output.String())
	}
}
