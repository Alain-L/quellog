# Corrections restantes à faire

## filtering-logs.md
- [x] Retirer les exemples avec --window tout seul (lignes ~50-70)
- [x] Enlever la section "Filter Order Impact" (pas vrai)
- [x] Retirer stdin support `quellog -` (ligne ~370)
- [x] Retirer section "Filter Logic" trop complexe OU simplifier
- [x] Ajouter exemples avec powa/temboard_agent pour dbname/dbuser
- [x] Disperser les tips dans le texte (déjà fait - tips sont dans des admonitions contextuelles)
- [x] Mettre "No filters = all logs" au début

## filtering-output.md
- [ ] RAS (looks good)

## default-report.md
- [x] Enlever toutes les sections "Interpreting"
- [x] Enlever toutes les sections "Tuning recommendations"
- [x] Enlever section "Error Classes (SQLSTATE)"
- [x] Vérifier format checkpoint reporting (correct)

## sql-reports.md
- [x] Corriger la forme des tableaux --sql-summary (vérifier output/text.go)
- [x] Vérifier que c'est bien la vraie sortie

## json-export.md
- [ ] Ajouter exemples de résultats pour les commandes jq
- [ ] Mettre de côté les exemples d'intégration (Prometheus, etc.) -> juste note "useful for"

## markdown-export.md
- [ ] Fixer le bug de formatage (liste numérotée à partir de 2.)
- [ ] Garder seulement "Basic Usage" et "Output Format"
- [ ] Retirer les autres sections

## formats.md
- [ ] Enlever mention 99.79% (ligne ~39 ou similaire)
- [ ] Vérifier que "client=" est correct partout
