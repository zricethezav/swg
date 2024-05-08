package matcher

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/dlclark/regexp2/syntax"
	"github.com/rrethy/ahocorasick"
)

const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorPurple = "\033[35m"
)

type regexpPlusRadius struct {
	reg    *regexp.Regexp
	radius int
}

type Matcher struct {
	acFilter        *ahocorasick.Matcher
	patternLookup   map[string][]regexpPlusRadius
	caseInsensitive bool
}

type Match struct {
	MatchedString string
	MatchedRegex  string
	PosBegin      int
	PosEnd        int
	FilePath      string
	LineNumber    int
	LineContent   string
}

func NewMatcher(res []string, overrideInf int, caseInsensitive bool) (*Matcher, error) {
	fp := &Matcher{patternLookup: make(map[string][]regexpPlusRadius), caseInsensitive: caseInsensitive}
	acWords := make([]string, 0)
	for _, re := range res {
		// Extremely dumb way to extract a buffer zone around string literals.
		// To confuse metaphores... think of this as a needle blast radius in a haystack.
		// Yuge kudos to Doug Clark for the regexp2 library.
		tree, err := syntax.Parse(re, syntax.RE2)
		if err != nil {
			return nil, err
		}
		code, err := syntax.Write(tree)
		if err != nil {
			return nil, err
		}

		scanner := bufio.NewScanner(strings.NewReader(tree.Dump()))
		radius := 0

		// Regular expressions to match 'Max' 'String' and 'One' values
		maxRegex := regexp.MustCompile(`Max = (\d+)`)
		stringRegex := regexp.MustCompile(`String = ([^"]+)\)`)
		oneRegex := regexp.MustCompile(`One(?:-I)?\(Ch`)

		for scanner.Scan() {
			line := scanner.Text()
			if strings.Contains(line, "Max = inf") {
				if overrideInf == -1 {
					// we can't handle infinities so set radius to -1 to signal that
					// we won't be attempting to find keyword regions
					radius = -1
					break
				}
				radius += overrideInf
			}

			// Extract 'Max' value using regex
			if maxMatch := maxRegex.FindStringSubmatch(line); maxMatch != nil {
				var max int
				fmt.Sscanf(maxMatch[1], "%d", &max)
				radius += max
			}

			// Extract string and calculate length using regex
			if stringMatch := stringRegex.FindStringSubmatch(line); stringMatch != nil {
				radius += len(stringMatch[1])
			}

			if oneRegex.MatchString(line) {
				radius++
			}
		}

		// pad radius by 1.5x
		radius = int(float64(radius) * 1.5)
		if fp.caseInsensitive {
			if !strings.HasPrefix(re, "(?i)") {
				re = "(?i)" + re
			}
		}

		reP := regexpPlusRadius{
			reg:    regexp.MustCompile(re),
			radius: radius,
		}

		for _, c := range code.Strings {
			if len(c) <= 3 {
				continue
			}
			k := string(c)
			if caseInsensitive {
				k = strings.ToLower(k)
			}
			acWords = append(acWords, k)
			fp.patternLookup[k] = append(fp.patternLookup[k], reP)
		}
	}

	fp.acFilter = ahocorasick.CompileStrings(acWords)
	return fp, nil
}

// BRO_SUSPEND
// bro_suspend

func (fp *Matcher) FindMatches(text string, filePath string) []Match {
	transformedText := text
	if fp.caseInsensitive {
		transformedText = strings.ToLower(text)
	}

	lines := strings.Split(text, "\n") // Split the original text into lines

	matchLookup := make(map[string]struct{})
	matches := make([]Match, 0)
	for _, match := range fp.acFilter.FindAllString(transformedText) {
		w := string(match.Word)
		for _, rp := range fp.patternLookup[w] {
			start := match.Index - rp.radius
			if start < 0 {
				start = 0
			}
			end := match.Index + len(w) + rp.radius
			if end > len(transformedText) {
				end = len(transformedText)
			}

			haystack := transformedText[start:end]                     // Use transformed text for regex operations
			fmt.Printf("Checking: [%d:%d] %s\n", start, end, haystack) // Debug output

			for _, m := range rp.reg.FindAllStringIndex(haystack, -1) {
				originalStart := start + m[0]
				originalEnd := start + m[1]

				key := fmt.Sprintf("%d:%d:%s", originalStart, originalEnd, rp.reg.String())
				if _, ok := matchLookup[key]; !ok {
					lineNumber, lineContent := findLineAndContent(lines, originalStart)
					fmt.Printf("Match found: Line %d [%d:%d] %s\n", lineNumber, originalStart, originalEnd, text[originalStart:originalEnd]) // Debug output
					matchLookup[key] = struct{}{}
					matches = append(matches, Match{
						MatchedString: text[originalStart:originalEnd],
						MatchedRegex:  rp.reg.String(),
						PosBegin:      originalStart,
						PosEnd:        originalEnd,
						FilePath:      filePath,
						LineNumber:    lineNumber,
						LineContent:   lineContent,
					})
				}
			}
		}
	}
	return matches
}

// Helper function to find the line number and content for a given index
func findLineAndContent(lines []string, index int) (int, string) {
	currentIndex := 0
	for i, line := range lines {
		// Update currentIndex to the end of this line (including newline character)
		nextIndex := currentIndex + len(line) + 1 // +1 for the newline character
		if index < nextIndex {
			return i + 1, line // Lines are 1-indexed
		}
		currentIndex = nextIndex
	}
	return -1, "" // Return -1 if no line is found (should not happen)
}

func processFile(path string, fp *Matcher, wg *sync.WaitGroup) {
	defer wg.Done()
	file, err := os.Open(path)
	if err != nil {
		fmt.Println("Error opening file:", err)
		return
	}
	defer file.Close()

	contents, err := io.ReadAll(file)
	if err != nil {
		fmt.Println("Error reading file:", err)
		return
	}

	matches := fp.FindMatches(string(contents), path)
	// printMatches(matches, path, false)
	if len(matches) != 0 {
		fmt.Println(path)
		for _, m := range matches {
			fmt.Printf("%d: %s\n", m.LineNumber, m.MatchedString)
		}
		fmt.Println()
	}
}

func printMatches(matches []Match, path string, noColor bool) {
	if len(matches) > 0 {
		fmt.Printf("%s%s%s\n", colorPurple, path, colorReset) // Print path in purple
		for _, m := range matches {
			lineContent := m.LineContent
			matchContent := m.MatchedString

			// Use strings.Index to find the match in the line
			matchIndex := strings.Index(lineContent, matchContent)
			if matchIndex == -1 || noColor {
				// Fall back to non-colored output if no match is found or color is disabled
				fmt.Printf("%s%d%s: %s\n", colorGreen, m.LineNumber, colorReset, lineContent)
				continue
			}

			// Print the line with highlighted match
			fmt.Printf("%s%d%s: %s%s%s%s%s : ",
				colorGreen, m.LineNumber, colorReset, // Line number in green
				lineContent[:matchIndex], // Part of the line before the match
				colorRed,                 // Start color red for the match
				lineContent[matchIndex:matchIndex+len(matchContent)], // The matched part
				colorReset, // Reset color after the match
				lineContent[matchIndex+len(matchContent):], // Part of the line after the match
			)
			fmt.Printf("%s%s %s\n", colorReset, m.MatchedRegex, m.MatchedString)
		}
		fmt.Println()
	}
}

func (fp *Matcher) SearchDir(dir string) error {
	var wg sync.WaitGroup

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			fmt.Println("Error walking files:", err)
			return err
		}

		// Skip symbolic links
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}

		// Skip directories and process only regular files
		if !info.IsDir() {
			wg.Add(1)
			go processFile(path, fp, &wg)
		}

		return nil
	})

	if err != nil {
		fmt.Println("Error walking directory:", err)
		return err
	}

	wg.Wait()
	return err
}
