package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"regexp"

	"github.com/dlclark/regexp2/syntax"
	"github.com/rrethy/ahocorasick"
)

type RegexpPlusRadius struct {
	reg    *regexp.Regexp
	Radius int
}

type FindPatterns struct {
	ahocMatcher   *ahocorasick.Matcher
	patternLookup map[string][]RegexpPlusRadius
}

type Match struct {
	MatchedString string
	MatchedRegex  string
	PosBegin      int
	PosEnd        int
}

func NewFindPatterns(res []string, overrideInf int) (*FindPatterns, error) {
	fp := &FindPatterns{patternLookup: make(map[string][]RegexpPlusRadius)}
	needles := make([]string, 0)
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
				if overrideInf == 0 {
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

		reP := RegexpPlusRadius{
			reg:    regexp.MustCompile(re),
			Radius: radius,
		}

		for _, c := range code.Strings {
			needles = append(needles, string(c))
			fp.patternLookup[string(c)] = append(fp.patternLookup[string(c)], reP)
		}
	}

	fp.ahocMatcher = ahocorasick.CompileStrings(needles)
	return fp, nil
}

func (fp *FindPatterns) FindMatches(text string) []Match {
	matchLookup := make(map[string]struct{})
	matches := make([]Match, 0)
	for _, match := range fp.ahocMatcher.FindAllString(text) {
		for _, rp := range fp.patternLookup[string(match.Word)] {

			// TODO revisit this logic
			if rp.Radius == -1 {
				// do normal regexp match, don't bother with radius
				for _, m := range rp.reg.FindAllStringIndex(text, -1) {
					matches = append(matches, Match{
						MatchedString: text[m[0]:m[1]],
						MatchedRegex:  rp.reg.String(),
						PosBegin:      m[0],
						PosEnd:        m[1],
					})
				}
				continue
			}

			start := match.Index - rp.Radius
			if start < 0 {
				start = 0
			}
			end := match.Index + len(match.Word) + rp.Radius
			if end > len(text) {
				end = len(text)
			}
			haystack := text[start:end]

			for _, m := range rp.reg.FindAllStringIndex(haystack, -1) {
				key := fmt.Sprintf("%d:%d:%s", start+m[0], start+m[1], rp.reg.String())
				if _, ok := matchLookup[key]; ok {
					continue
				}
				matchLookup[key] = struct{}{}
				matches = append(matches, Match{
					MatchedString: haystack[m[0]:m[1]],
					MatchedRegex:  rp.reg.String(),
					PosBegin:      start + m[0],
					PosEnd:        start + m[1],
				})
			}
		}
	}
	return matches
}

func processFile(path string, fp *FindPatterns, wg *sync.WaitGroup) {
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

	matches := fp.FindMatches(string(contents))
	for _, m := range matches {
		fmt.Printf("Matched: %s, Regex: %s, Begin: %d, End: %d\n", m.MatchedString, m.MatchedRegex, m.PosBegin, m.PosEnd)

	}
}

func main() {
	if len(os.Args) != 2 {
		fmt.Println("Usage: ./findpatterns '<pattern>'")
		return
	}

	patterns := []string{os.Args[1]}
	fp, err := NewFindPatterns(patterns, 100)
	if err != nil {
		fmt.Println("Error setting up patterns:", err)
		return
	}

	var wg sync.WaitGroup

	err = filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			fmt.Println("Error walking files:", err)
			return err
		}
		if !info.IsDir() {
			wg.Add(1)
			go processFile(path, fp, &wg)
		}
		return nil
	})

	if err != nil {
		fmt.Println("Error walking directory:", err)
		return
	}

	wg.Wait()
}
