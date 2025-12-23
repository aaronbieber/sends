package main

import (
	"bufio"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Send struct {
	Color string
	Grade string
	Meta  string
	Date  string
}

type Frontmatter struct {
	Date  string   `yaml:"date"`
	Sends []string `yaml:"sends"`
}

// parseGrade extracts numeric value for sorting
// Sorting order: point grades (900, 1000, ...) < unknown grades (?, ??, 5.?) < rope grades (5.x) < boulder grades (Vx)
func parseGrade(grade string) float64 {
	// Handle question marks and unknown grades
	if strings.Contains(grade, "?") {
		return 10000.0 // Sort after point grades but before rope grades
	}

	// Handle V-grades (boulder grades)
	if strings.HasPrefix(grade, "V") {
		g := strings.TrimPrefix(grade, "V")
		hasPlus := strings.HasSuffix(g, "+")
		hasMinus := strings.HasSuffix(g, "-")
		g = strings.TrimSuffix(g, "+")
		g = strings.TrimSuffix(g, "-")

		val, err := strconv.ParseFloat(g, 64)
		if err != nil {
			return 1000000.0 // Sort unknown V-grades last
		}

		// Add 100000 to separate V-grades from rope grades
		val += 100000.0

		// Add small amounts for modifiers
		if hasPlus {
			val += 0.1
		} else if hasMinus {
			val -= 0.1
		}

		return val
	}

	// Handle rope grades (5.x format)
	if strings.HasPrefix(grade, "5.") {
		g := strings.TrimPrefix(grade, "5.")
		hasPlus := strings.HasSuffix(g, "+")
		hasMinus := strings.HasSuffix(g, "-")
		g = strings.TrimSuffix(g, "+")
		g = strings.TrimSuffix(g, "-")

		val, err := strconv.ParseFloat(g, 64)
		if err != nil {
			return 10000.0 // Sort unknown rope grades with question marks
		}

		// Add 20000 to separate rope grades from point grades
		val += 20000.0

		// Add small amounts for modifiers
		if hasPlus {
			val += 0.1
		} else if hasMinus {
			val -= 0.1
		}

		return val
	}

	// Handle point grades (pure numbers like 900, 1000, 1100)
	val, err := strconv.ParseFloat(grade, 64)
	if err != nil {
		return 1000000.0 // Sort unknown grades last
	}

	return val
}

func extractFrontmatter(path string) (*Frontmatter, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	// Extract frontmatter between --- delimiters
	scanner := bufio.NewScanner(file)
	var frontmatterLines []string
	inFrontmatter := false
	delimiterCount := 0

	for scanner.Scan() {
		line := scanner.Text()
		if line == "---" {
			delimiterCount++
			if delimiterCount == 1 {
				inFrontmatter = true
				continue
			} else if delimiterCount == 2 {
				break
			}
		}
		if inFrontmatter {
			frontmatterLines = append(frontmatterLines, line)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	// Parse YAML
	var fm Frontmatter
	yamlStr := strings.Join(frontmatterLines, "\n")
	if err := yaml.Unmarshal([]byte(yamlStr), &fm); err != nil {
		return nil, err
	}

	return &fm, nil
}

func main() {
	// CLI flags - define both short and long forms
	var contentType string
	var countMode bool
	var datesGrade string

	flag.StringVar(&contentType, "t", "posts", "content type to parse")
	flag.StringVar(&contentType, "type", "posts", "content type to parse")
	flag.BoolVar(&countMode, "c", false, "output counts instead of list")
	flag.BoolVar(&countMode, "count", false, "output counts instead of list")
	flag.StringVar(&datesGrade, "d", "", "output unique dates for posts with this grade")
	flag.StringVar(&datesGrade, "dates", "", "output unique dates for posts with this grade")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: sends [options] <hugo-site-path>\n")
		fmt.Fprintf(os.Stderr, "\nOptions:\n")
		fmt.Fprintf(os.Stderr, "  -t, --type string   content type to parse (default \"posts\")\n")
		fmt.Fprintf(os.Stderr, "  -c, --count         output counts instead of list\n")
		fmt.Fprintf(os.Stderr, "  -d, --dates string  output unique dates for posts with this grade\n")
	}

	flag.Parse()

	if flag.NArg() < 1 {
		flag.Usage()
		os.Exit(1)
	}

	sitePath := flag.Arg(0)
	contentPath := filepath.Join(sitePath, "content", contentType)

	// Check if content path exists
	if _, err := os.Stat(contentPath); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Error: content path does not exist: %s\n", contentPath)
		os.Exit(1)
	}

	// Regex pattern matches the bash scripts
	pattern := regexp.MustCompile(`(?P<color>[\w\s']*?\s?)(?P<grade>V?[\d.+?-]+)(?P<meta>\s?.*)`)

	var sends []Send

	// Walk directory to find all index.md files
	err := filepath.WalkDir(contentPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if !d.IsDir() && strings.ToLower(d.Name()) == "index.md" {
			fm, err := extractFrontmatter(path)
			if err != nil {
				// Skip files with parse errors
				return nil
			}

			// Parse each send string with regex
			for _, sendStr := range fm.Sends {
				matches := pattern.FindStringSubmatch(sendStr)
				if matches != nil {
					sends = append(sends, Send{
						Color: matches[1],
						Grade: matches[2],
						Meta:  matches[3],
						Date:  fm.Date,
					})
				}
			}
		}

		return nil
	})

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error walking directory: %v\n", err)
		os.Exit(1)
	}

	// Sort sends by grade (numeric), then by color
	sort.SliceStable(sends, func(i, j int) bool {
		gi := parseGrade(sends[i].Grade)
		gj := parseGrade(sends[j].Grade)
		if gi != gj {
			return gi < gj
		}
		return sends[i].Color < sends[j].Color
	})

	if datesGrade != "" {
		// Dates mode: filter by grade and output unique dates chronologically
		dateMap := make(map[string]bool)
		var dates []string

		// Collect unique dates for the specified grade
		for _, send := range sends {
			if send.Grade == datesGrade && send.Date != "" {
				if !dateMap[send.Date] {
					dateMap[send.Date] = true
					dates = append(dates, send.Date)
				}
			}
		}

		// Sort dates chronologically
		sort.Slice(dates, func(i, j int) bool {
			ti, erri := time.Parse("2006-01-02", dates[i])
			tj, errj := time.Parse("2006-01-02", dates[j])
			// If parsing fails, fall back to string comparison
			if erri != nil || errj != nil {
				return dates[i] < dates[j]
			}
			return ti.Before(tj)
		})

		// Output dates in ISO format (YYYY-MM-DD)
		for _, date := range dates {
			fmt.Println(date)
		}
	} else if countMode {
		// Count mode: group by grade and count
		counts := make(map[string]int)
		var gradeOrder []string
		seen := make(map[string]bool)

		for _, send := range sends {
			counts[send.Grade]++
			if !seen[send.Grade] {
				gradeOrder = append(gradeOrder, send.Grade)
				seen[send.Grade] = true
			}
		}

		// Output counts
		for _, grade := range gradeOrder {
			fmt.Printf("%7d %s\n", counts[grade], grade)
		}
	} else {
		// List mode: output formatted sends
		for _, send := range sends {
			fmt.Printf("%s%s%s\n", send.Color, send.Grade, send.Meta)
		}
	}
}
