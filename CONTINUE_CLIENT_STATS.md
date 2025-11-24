# Client Statistics Reporting - Work in Progress

## Context

**Branch**: `client-stats-reporting` (created from `feature`)
**Commit**: `53b26f2` - wip: add count maps to UniqueEntityMetrics for proportion reporting

**Goal**: Add proportion-based statistics reporting for database entities (users, apps, databases, hosts) in pgBadger style.

Example output desired:
```
TOP USERS
  app_user        82  41.6%
  admin           45  22.8%
  readonly        38  19.3%
```

## Progress Summary

### ✅ Step 1: Structure Modification (COMPLETED)
Modified `analysis/summary.go` to add count maps to `UniqueEntityMetrics`:

```go
type UniqueEntityMetrics struct {
    // Existing fields...
    UniqueDbs   int
    UniqueUsers int
    UniqueApps  int
    UniqueHosts int
    DBs         []string
    Users       []string
    Apps        []string
    Hosts       []string

    // NEW: Occurrence counts (commit 53b26f2)
    DBCounts    map[string]int
    UserCounts  map[string]int
    AppCounts   map[string]int
    HostCounts  map[string]int
}
```

### 🔄 Step 2: Collection Logic (TODO)
Need to modify `UniqueEntityAnalyzer` in `analysis/summary.go` (lines 283-415):

#### 2.1 Add count maps to the struct
```go
type UniqueEntityAnalyzer struct {
    dbSet   map[string]struct{}
    userSet map[string]struct{}
    appSet  map[string]struct{}
    hostSet map[string]struct{}

    // ADD THESE:
    dbCounts   map[string]int
    userCounts map[string]int
    appCounts  map[string]int
    hostCounts map[string]int
}
```

#### 2.2 Initialize in NewUniqueEntityAnalyzer() (line 291)
```go
func NewUniqueEntityAnalyzer() *UniqueEntityAnalyzer {
    return &UniqueEntityAnalyzer{
        dbSet:   make(map[string]struct{}, 100),
        userSet: make(map[string]struct{}, 100),
        appSet:  make(map[string]struct{}, 100),
        hostSet: make(map[string]struct{}, 100),
        // ADD THESE:
        dbCounts:   make(map[string]int, 100),
        userCounts: make(map[string]int, 100),
        appCounts:  make(map[string]int, 100),
        hostCounts: make(map[string]int, 100),
    }
}
```

#### 2.3 Increment counters in Process() (lines 307-401)
In the `Process()` method, whenever an entity is added to a set, also increment its counter:

```go
// Example for database extraction (around line 350)
if dbName := extractValueAt(msg, eqIdx+1); dbName != "" {
    a.dbSet[dbName] = struct{}{}
    a.dbCounts[dbName]++  // ADD THIS
    matched = true
}

// Example for user extraction (around line 364)
if userName := extractValueAt(msg, eqIdx+1); userName != "" {
    a.userSet[userName] = struct{}{}
    a.userCounts[userName]++  // ADD THIS
    matched = true
}

// Apply same pattern for apps and hosts
```

#### 2.4 Populate count maps in Finalize() (lines 404-415)
```go
func (a *UniqueEntityAnalyzer) Finalize() UniqueEntityMetrics {
    return UniqueEntityMetrics{
        UniqueDbs:   len(a.dbSet),
        UniqueUsers: len(a.userSet),
        UniqueApps:  len(a.appSet),
        UniqueHosts: len(a.hostSet),
        DBs:         mapKeysAsSlice(a.dbSet),
        Users:       mapKeysAsSlice(a.userSet),
        Apps:        mapKeysAsSlice(a.appSet),
        Hosts:       mapKeysAsSlice(a.hostSet),
        // ADD THESE:
        DBCounts:    a.dbCounts,
        UserCounts:  a.userCounts,
        AppCounts:   a.appCounts,
        HostCounts:  a.hostCounts,
    }
}
```

### ⏳ Step 3: Output Formatting (TODO)

#### 3.1 Add sorting helper in analysis/summary.go or analysis/utils.go
```go
// EntityCount holds an entity name with its count for sorting
type EntityCount struct {
    Name  string
    Count int
}

// SortByCount sorts entity counts in descending order and returns top N
func SortByCount(counts map[string]int, limit int) []EntityCount {
    items := make([]EntityCount, 0, len(counts))
    for name, count := range counts {
        items = append(items, EntityCount{Name: name, Count: count})
    }

    sort.Slice(items, func(i, j int) bool {
        if items[i].Count != items[j].Count {
            return items[i].Count > items[j].Count  // descending
        }
        return items[i].Name < items[j].Name  // alphabetical tie-breaker
    })

    if limit > 0 && len(items) > limit {
        items = items[:limit]
    }

    return items
}
```

#### 3.2 Modify output/text.go (around lines 369-397)
Current code shows simple lists. Replace with proportion-based display:

```go
// Example for displaying top users with proportions
if metrics.UniqueEntities.UniqueUsers > 0 {
    fmt.Fprintln(w, "\n  TOP USERS")
    totalLogs := metrics.Global.Count
    topUsers := SortByCount(metrics.UniqueEntities.UserCounts, 10)  // top 10

    for _, item := range topUsers {
        percentage := float64(item.Count) * 100.0 / float64(totalLogs)
        fmt.Fprintf(w, "    %-25s %6d  %5.1f%%\n",
            item.Name, item.Count, percentage)
    }
}

// Apply same pattern for DBs, Apps, Hosts
```

#### 3.3 Update output/json.go
The JSON output should automatically include the new count maps since they're part of the struct.

#### 3.4 Update output/markdown.go
Add similar proportion display in markdown table format.

### ⏳ Step 4: Testing (TODO)
```bash
# Build and test
go build -o bin/quellog .
bin/quellog test/testdata/test_summary.log

# Verify output shows proportions
# Look for sections like:
#   TOP USERS
#     app_user        82  41.6%
#     ...

# Run tests
go test ./...
```

### 🎯 Step 5: Update Golden Files (TODO - if needed)
If tests fail due to JSON structure changes:
```bash
# Review changes carefully first
bin/quellog test/testdata/test_summary.log --json > /tmp/new_golden.json
# Inspect differences
# If correct, update golden file
```

## Quick Resume Commands

```bash
# Switch to branch
git checkout client-stats-reporting

# Check current state
git log --oneline -3
git diff feature

# Edit files
code analysis/summary.go  # Step 2
code output/text.go       # Step 3

# Build and test
go build -o bin/quellog .
bin/quellog test/testdata/test_summary.log

# Run tests
go test ./...
```

## Files to Modify

1. **analysis/summary.go** (lines 283-415)
   - UniqueEntityAnalyzer struct
   - NewUniqueEntityAnalyzer()
   - Process() method
   - Finalize() method

2. **analysis/utils.go** or **analysis/summary.go**
   - Add SortByCount() helper function

3. **output/text.go** (around lines 369-397)
   - Replace simple lists with proportion display
   - Show top 10 entities with count + percentage

4. **output/markdown.go**
   - Update markdown output format

5. **output/json.go**
   - Should work automatically with struct changes

## Testing Strategy

1. **Functional test**: Run on test_summary.log and verify:
   - All entities are counted correctly
   - Percentages sum correctly
   - Top entities are shown in descending order
   - Format matches pgBadger style

2. **Unit tests**: Check if any existing tests break
   ```bash
   go test ./analysis/... -v
   go test ./output/... -v
   ```

3. **Real data test**: Try on _random_logs samples
   ```bash
   bin/quellog _random_logs/samples/A.log
   bin/quellog _random_logs/samples/C.csv
   ```

## Expected Changes Summary

- **analysis/summary.go**: ~30 lines added (struct fields, counter increments, map population)
- **analysis/utils.go**: ~25 lines added (sorting helper)
- **output/text.go**: ~40 lines modified (replace 4 entity sections)
- **output/markdown.go**: ~20 lines modified
- **Tests**: May need golden file updates

## Reference: pgBadger Style

The target output format:
```
TOP USERS
  app_user        82  41.6%
  admin           45  22.8%
  readonly        38  19.3%

TOP DATABASES
  prod_db        145  73.6%
  test_db         35  17.8%
  dev_db          17   8.6%
```

Features:
- Left-aligned entity name (25 chars width)
- Right-aligned count (6 chars)
- Right-aligned percentage (5.1f format)
- Sorted by count descending
- Show top 10 only
