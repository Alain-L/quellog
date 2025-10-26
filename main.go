package main

import (
	"dalibo/quellog/cmd"
)

func main() {
	// // Création d'un fichier pour le profil CPU
	// f, err := os.Create("cpu.prof")
	// if err != nil {
	// 	log.Fatal(err)
	// }
	// defer f.Close()
	// // Démarrer le profilage CPU
	// if err := pprof.StartCPUProfile(f); err != nil {
	// 	log.Fatal(err)
	// }
	// defer pprof.StopCPUProfile()

	// Exécuter l'application
	cmd.Execute()
}
