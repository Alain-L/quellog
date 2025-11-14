# Corrections restantes à faire

## filtering-logs.md
- [ ] Retirer les exemples avec --window tout seul (lignes ~50-70)
- [ ] Enlever la section "Filter Order Impact" (pas vrai)
- [ ] Retirer stdin support `quellog -` (ligne ~370)
- [ ] Retirer section "Filter Logic" trop complexe OU simplifier
- [ ] Ajouter exemples avec powa/temboard_agent pour dbname/dbuser
- [ ] Disperser les tips dans le texte
- [ ] Mettre "No filters = all logs" au début

## filtering-output.md
- [ ] RAS (looks good)

## default-report.md
- [ ] Enlever toutes les sections "Interpreting"
- [ ] Enlever toutes les sections "Tuning recommendations"
- [ ] Enlever section "Error Classes (SQLSTATE)"
- [ ] Vérifier format checkpoint reporting

## sql-reports.md
- [ ] Corriger la forme des tableaux --sql-summary (vérifier output/text.go)
- [ ] Vérifier que c'est bien la vraie sortie

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
