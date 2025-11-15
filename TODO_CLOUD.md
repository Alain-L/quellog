# TODO Cloud - Tests et améliorations quellog

**Objectif** : Tester et améliorer quellog avec des logs PostgreSQL réels issus des 3 plateformes cloud managées (GCP Cloud SQL, AWS RDS, Azure Database).

**Statut CLI** : ✅ GCP, AWS, Azure opérationnels

---

## 🎯 Phase 1 - Création d'instances de test

### GCP Cloud SQL
- [ ] Créer instance PostgreSQL 15/16 (tier `db-f1-micro` pour économiser)
- [ ] Configurer les logs : `log_statement = 'all'`, `log_duration = on`, `log_checkpoints = on`
- [ ] Activer Cloud Logging pour récupération automatique
- [ ] Tester les formats de logs : stderr vs jsonPayload

**Commandes** :
```bash
# Création instance
gcloud sql instances create quellog-test-pg \
  --database-version=POSTGRES_16 \
  --tier=db-f1-micro \
  --region=europe-west1 \
  --database-flags=log_statement=all,log_duration=on,log_checkpoints=on \
  --project=serious-ascent-478222-c9

# Récupération logs
gcloud logging read "resource.type=cloudsql_database" \
  --project=serious-ascent-478222-c9 \
  --format=json > gcp_cloudsql_logs.json
```

### AWS RDS PostgreSQL
- [ ] Créer instance PostgreSQL 16 (classe `db.t3.micro` free tier)
- [ ] Activer Enhanced Monitoring
- [ ] Configurer Parameter Group avec logging verbeux
- [ ] Tester récupération logs via `aws rds download-db-log-file-portion`

**Commandes** :
```bash
# Création instance
aws rds create-db-instance \
  --db-instance-identifier quellog-test-pg \
  --db-instance-class db.t3.micro \
  --engine postgres \
  --engine-version 16.6 \
  --master-username postgres \
  --master-user-password 'TestQuellog2025!' \
  --allocated-storage 20 \
  --region eu-west-3

# Lister logs disponibles
aws rds describe-db-log-files --db-instance-identifier quellog-test-pg --region eu-west-3

# Télécharger logs
aws rds download-db-log-file-portion \
  --db-instance-identifier quellog-test-pg \
  --log-file-name error/postgresql.log \
  --region eu-west-3 \
  --output text > aws_rds_logs.log
```

### Azure Database for PostgreSQL
- [ ] Créer Flexible Server PostgreSQL 16
- [ ] Configurer Server Parameters pour logging
- [ ] Activer diagnostic logs
- [ ] Tester `az postgres flexible-server server-logs download`

**Commandes** :
```bash
# Création resource group
az group create --name quellog-test-rg --location francecentral

# Création instance
az postgres flexible-server create \
  --name quellog-test-pg \
  --resource-group quellog-test-rg \
  --location francecentral \
  --admin-user pgadmin \
  --admin-password 'TestQuellog2025!' \
  --sku-name Standard_B1ms \
  --tier Burstable \
  --version 16

# Lister logs
az postgres flexible-server server-logs list \
  --name quellog-test-pg \
  --resource-group quellog-test-rg

# Télécharger logs
az postgres flexible-server server-logs download \
  --name quellog-test-pg \
  --resource-group quellog-test-rg \
  --name postgresql-2025-11-17_000000.log
```

---

## 🧪 Phase 2 - Génération de workload test

### Scénarios de test
- [ ] **Workload OLTP** : Beaucoup d'INSERT/UPDATE/DELETE rapides
- [ ] **Workload OLAP** : Requêtes analytiques longues avec agrégations
- [ ] **Workload mixte** : Mix lecture/écriture
- [ ] **Stress test** : Connexions multiples, transactions concurrentes
- [ ] **Maintenance** : VACUUM, ANALYZE, REINDEX, CHECKPOINT forcés

### Scripts de génération
```bash
# pgbench standard
pgbench -i -s 10 postgres://user@host/db
pgbench -c 10 -j 2 -t 1000 postgres://user@host/db

# Requêtes complexes custom
psql -h <host> -U postgres -c "
  SELECT pg_sleep(5);  -- Requête lente
  VACUUM ANALYZE;       -- Maintenance
  CREATE INDEX ...;     -- DDL
"
```

**À générer** :
- [ ] Script `generate_oltp_workload.sh`
- [ ] Script `generate_olap_workload.sh`
- [ ] Script `generate_maintenance_events.sh`
- [ ] Script `generate_errors.sh` (connexions échouées, deadlocks, etc.)

---

## 📊 Phase 3 - Tests formats de logs spécifiques cloud

### GCP Cloud SQL
- [ ] Format JSON natif de Cloud Logging (jsonPayload)
- [ ] Logs stderr classiques
- [ ] Logs avec metadata GCP (project_id, instance_id, etc.)
- [ ] Tester parsing de `protoPayload` pour les opérations admin

**Spécificités à tester** :
- Champs GCP : `resource.labels.database_id`, `severity`, `timestamp`
- Logs d'audit (connexions, CREATE/DROP DATABASE)
- Slow query logs dans Cloud Monitoring

### AWS RDS
- [ ] Format CSV (via Enhanced Monitoring)
- [ ] Format stderr standard
- [ ] Logs CloudWatch integration
- [ ] Performance Insights logs

**Spécificités à tester** :
- Logs splittés par heure (`postgresql.log.2025-11-17-10`, etc.)
- Logs compressés (`.gz`)
- Rotation automatique des logs
- Enhanced Monitoring JSON

### Azure Database
- [ ] Format syslog
- [ ] Diagnostic logs (JSON)
- [ ] Logs via Azure Monitor
- [ ] Log Analytics integration

**Spécificités à tester** :
- Logs avec `_ResourceId`, `OperationName`
- Integration avec Kusto Query Language (KQL)
- Server logs vs diagnostic logs

---

## 🔧 Phase 4 - Améliorations quellog

### Support formats cloud (détection automatique)
- [ ] **Auto-détection format GCP** : Reconnaître JSON Cloud Logging (jsonPayload, severity, resource)
- [ ] **Auto-détection format AWS** : Reconnaître Enhanced Monitoring JSON, logs CSV
- [ ] **Auto-détection format Azure** : Reconnaître syslog + metadata Azure (_ResourceId, OperationName)
- [ ] **Extraction automatique metadata cloud** : instance_id, région, projet, resource_id

### 🌊 Streaming depuis le cloud (PRIORITAIRE)

**Concept** : Au lieu de télécharger puis analyser, streamer directement depuis les APIs cloud vers quellog.

#### GCP Cloud Logging → quellog
```bash
# Stream en temps réel
gcloud logging read "resource.type=cloudsql_database" \
  --format=json \
  --project=serious-ascent-478222-c9 \
  --freshness=1d | quellog --stdin --format=gcp-json

# Avec filtre temporel
gcloud logging read "resource.type=cloudsql_database AND timestamp>=\"2025-11-17T00:00:00Z\"" \
  --format=json --project=serious-ascent-478222-c9 | quellog --stdin
```

**Implémentation** :
- [ ] Ajouter flag `--stdin` pour lire depuis stdin
- [ ] Parser JSON array streamé (logs viennent ligne par ligne)
- [ ] Extraire `jsonPayload.message` ou `textPayload` selon le type
- [ ] Parser metadata : `resource.labels.database_id`, `severity`, `timestamp`

#### AWS CloudWatch Logs / RDS → quellog
```bash
# Stream logs RDS en temps réel
aws logs tail /aws/rds/instance/quellog-test-pg/postgresql \
  --follow \
  --format short | quellog --stdin

# Ou via RDS API (pagination automatique)
aws rds download-db-log-file-portion \
  --db-instance-identifier quellog-test-pg \
  --log-file-name error/postgresql.log \
  --output text \
  --region eu-west-3 | quellog --stdin
```

**Implémentation** :
- [ ] Support streaming ligne par ligne (pas besoin de tout buffer)
- [ ] Gérer les logs splittés par heure automatiquement
- [ ] Détecter Enhanced Monitoring JSON vs logs text

#### Azure Monitor → quellog
```bash
# Stream depuis Log Analytics
az monitor log-analytics query \
  --workspace <workspace-id> \
  --analytics-query "AzureDiagnostics | where ResourceType == 'POSTGRESQL'" \
  --output json | quellog --stdin --format=azure-json

# Stream depuis server logs
az postgres flexible-server server-logs download \
  --name quellog-test-pg \
  --resource-group quellog-test-rg \
  --name postgresql.log --output none | quellog --stdin
```

**Implémentation** :
- [ ] Parser JSON Log Analytics (tables, colonnes dynamiques)
- [ ] Extraire `Message_s` ou `RawData` selon la query
- [ ] Support syslog format natif Azure

### Avantages du streaming
✅ **Pas de téléchargement** : Analyse directe, économie disque
✅ **Temps réel** : Possibilité de `--follow` pour monitoring live
✅ **Efficace** : Traitement incrémental, pas besoin de tout charger en RAM
✅ **Sécurité** : Logs jamais stockés localement
✅ **Intégration native** : Utilise les APIs/CLI officielles

### Difficulté d'implémentation : 🟢 FACILE

**Pourquoi c'est simple** :
- Go a déjà `os.Stdin` natif
- Parsers existants lisent ligne par ligne (déjà streamable)
- Les CLIs cloud supportent `--output text` ou `--format json` vers stdout
- Juste besoin d'ajouter un check : si `filename == "-"` ou flag `--stdin` → lire depuis stdin

**Changements minimes** :
```go
// Dans cmd/root.go
rootCmd.PersistentFlags().Bool("stdin", false, "Read logs from stdin")

// Dans cmd/execute.go
func executeParsing(cmd *cobra.Command, args []string) {
    if useStdin {
        // Parse from os.Stdin instead of files
        parser.ParseReader(os.Stdin, out)
    } else {
        // Existing file parsing logic
    }
}
```

**POC en 1h max** ! 🚀

### 📝 Features essentielles à implémenter

#### 1. Support stdin
- [ ] Flag `--stdin` ou accepter `"-"` comme filename
- [ ] Lire depuis `os.Stdin` au lieu d'un fichier
- [ ] Support pipe : `gcloud logging read ... | quellog --stdin`
- [ ] Détection automatique du format depuis stdin (JSON, CSV, text)

#### 2. Time ranges standards (à la cloud CLI)
- [ ] Flag `--last` avec durées standards : `1h`, `24h`, `7d`, `30d`
- [ ] Syntaxe intuitive : `quellog --last=1h` au lieu de `--begin "2025-11-17 21:00:00"`
- [ ] Mapping automatique vers --begin/--end

**Exemples d'utilisation** :
```bash
# Dernière heure
quellog --last=1h logs.json
gcloud logging read ... --freshness=1h | quellog --stdin --last=1h

# Dernier jour
quellog --last=24h /var/log/postgresql/*.log

# Dernière semaine
quellog --last=7d archive.tar.gz

# Dernier mois
quellog --last=30d /path/to/logs/
```

**Standards à supporter** :
- `1h`, `2h`, `6h`, `12h`, `24h` (heures)
- `1d`, `7d`, `30d`, `90d` (jours)
- `1w`, `2w`, `4w` (semaines)
- `1m`, `3m`, `6m`, `12m` (mois approximatifs : 30j)

**Implémentation** :
```go
// Parse --last flag et convertir en begin/end
func parseLastDuration(last string) (begin, end time.Time) {
    duration, err := time.ParseDuration(last) // Go supporte "1h", "24h"
    if err != nil {
        // Gérer "7d", "30d" manuellement
        duration = parseCustomDuration(last)
    }
    end = time.Now()
    begin = end.Add(-duration)
    return begin, end
}
```

**Priorité** : 🔴 **HAUTE** - Améliore drastiquement l'UX pour les logs cloud

### Nouvelles features cloud
- [ ] **Extraction metadata cloud** : Afficher instance_id, région, projet dans le summary
- [ ] **Support logs compressés cloud** : `.gz` splittés par heure (AWS)
- [ ] **Filter par severity cloud** : Map `INFO/WARNING/ERROR` → log levels PostgreSQL
- [ ] **Timeline cloud events** : Afficher backups, failovers, maintenance windows

### Optimisations
- [ ] Parser streaming incrémental (pas besoin de tout lire d'un coup)
- [ ] Support logs multi-fichiers horodatés (AWS : `postgresql.log.2025-11-*`)
- [ ] Agrégation cross-instance (analyser plusieurs instances à la fois)
- [ ] Export vers formats cloud natives (BigQuery, CloudWatch Insights, Log Analytics)

---

## 📈 Phase 5 - Benchmarks & Comparaisons

### Performance quellog vs outils cloud
- [ ] Comparer avec `gcloud logging read` (vitesse, features)
- [ ] Comparer avec AWS CloudWatch Insights
- [ ] Comparer avec Azure Log Analytics
- [ ] Comparer avec pgBadger sur logs cloud

### Métriques à mesurer
- [ ] Temps de parsing pour 1GB, 10GB, 100GB de logs
- [ ] Mémoire utilisée
- [ ] Précision des analyses (SQL queries détectées, erreurs, etc.)
- [ ] Features manquantes vs outils cloud natifs

### Datasets de test
- [ ] **Small** : 100MB de logs (1h de prod légère)
- [ ] **Medium** : 1GB de logs (1 jour de prod normale)
- [ ] **Large** : 10GB de logs (1 semaine de prod intense)
- [ ] **XLarge** : 100GB+ (1 mois de prod multi-instance)

---

## 🚀 Phase 6 - Automation & CI/CD

### Scripts d'automatisation
- [ ] `cloud_test_suite.sh` : Déploie instances sur les 3 clouds, génère workload, récupère logs, analyse
- [ ] `compare_clouds.sh` : Compare les formats/logs des 3 providers
- [ ] `stress_test.sh` : Génère 100GB de logs et mesure perfs quellog

### GitHub Actions
- [ ] Workflow hebdomadaire : Déploie instances cloud, teste quellog, cleanup
- [ ] Tests de régression sur logs cloud réels
- [ ] Badges avec support "GCP ✅ AWS ✅ Azure ✅"

### Documentation
- [ ] `docs/cloud-providers.md` : Guide d'utilisation avec GCP/AWS/Azure
- [ ] `docs/cloud-log-formats.md` : Spécificités de chaque format cloud
- [ ] Exemples dans README : Comment analyser logs GCP/AWS/Azure

---

## 💡 Idées avancées

### Intégrations cloud natives
- [ ] Publier quellog comme Cloud Run service (GCP)
- [ ] Lambda function (AWS) pour analyse à la volée
- [ ] Azure Function pour processing de logs
- [ ] Container image optimisé pour Cloud environments

### Analyse prédictive
- [ ] Détecter patterns d'incidents dans logs cloud
- [ ] Alerting sur anomalies (spike de slow queries, erreurs inhabituelles)
- [ ] Correlation avec métriques cloud (CPU, RAM, IOPS)

### Export & Visualisation
- [ ] Export vers BigQuery pour analytics SQL
- [ ] Export vers CloudWatch dashboard
- [ ] Export vers Azure Workbooks
- [ ] Grafana datasource plugin

---

## 🗓️ Planning suggéré

### Session 1 (Dimanche) - Setup & Discovery
- Créer 1 instance sur chaque cloud (GCP, AWS, Azure)
- Générer du workload basique (pgbench)
- Streamer premiers logs vers quellog (tester le pipe)
- Identifier les différences de formats

### Session 2 - Streaming & Time ranges
- **Implémenter support `--stdin`** (priorité haute)
- **Implémenter flag `--last`** (1h, 24h, 7d, 30d, etc.)
- Auto-détection format JSON GCP (jsonPayload)
- Auto-détection format AWS (text vs JSON)
- Auto-détection format Azure (syslog + metadata)
- Créer tests unitaires

### Session 3 - Features & Optimisations
- Extraction automatique metadata cloud
- Support streaming incrémental (pas de buffer total)
- Timeline cloud events (backups, failovers)
- Tests de performance streaming vs download

### Session 4 - Automation
- Scripts de test automatisés
- Benchmarks vs outils natifs
- Documentation complète
- Cleanup & optimisations finales

---

## 📝 Notes & Contraintes

### Coûts cloud (estimation)
- **GCP** : db-f1-micro ~$7/mois (free tier possible)
- **AWS** : db.t3.micro free tier 750h/mois (gratuit 1ère année)
- **Azure** : B1ms ~$12/mois (free tier 12 mois)

**💡 Astuce** : Toujours supprimer les instances après tests pour éviter les frais !

### Commandes de cleanup
```bash
# GCP
gcloud sql instances delete quellog-test-pg --project=serious-ascent-478222-c9

# AWS
aws rds delete-db-instance --db-instance-identifier quellog-test-pg --skip-final-snapshot --region eu-west-3

# Azure
az group delete --name quellog-test-rg --yes
```

---

**Prêt pour dimanche ! 🚀**
