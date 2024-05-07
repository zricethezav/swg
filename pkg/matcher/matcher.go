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

type regexpPlusRadius struct {
	reg    *regexp.Regexp
	radius int
}

type Matcher struct {
	acFilter      *ahocorasick.Matcher
	patternLookup map[string][]regexpPlusRadius
}

type Match struct {
	MatchedString string
	MatchedRegex  string
	PosBegin      int
	PosEnd        int
	FilePath      string
}

func NewMatcher(res []string, overrideInf int) (*Matcher, error) {
	fp := &Matcher{patternLookup: make(map[string][]regexpPlusRadius)}
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
		fmt.Println("Radius:", radius)

		reP := regexpPlusRadius{
			reg:    regexp.MustCompile(re),
			radius: radius,
		}

		for _, c := range code.Strings {
			acWords = append(acWords, string(c))
			fp.patternLookup[string(c)] = append(fp.patternLookup[string(c)], reP)
		}
	}

	fp.acFilter = ahocorasick.CompileStrings(acWords)
	return fp, nil
}

func (fp *Matcher) FindMatches(text string, filePath string) []Match {
	matchLookup := make(map[string]struct{})
	matches := make([]Match, 0)
	for _, match := range fp.acFilter.FindAllString(text) {
		for _, rp := range fp.patternLookup[string(match.Word)] {
			// TODO revisit this logic
			if rp.radius == -1 {
				// do normal regexp match, don't bother with radius
				for _, m := range rp.reg.FindAllStringIndex(text, -1) {
					matches = append(matches, Match{
						MatchedString: text[m[0]:m[1]],
						MatchedRegex:  rp.reg.String(),
						PosBegin:      m[0],
						PosEnd:        m[1],
						FilePath:      filePath,
					})
				}
				continue
			}

			start := match.Index - rp.radius
			if start < 0 {
				start = 0
			}
			end := match.Index + len(match.Word) + rp.radius
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
					FilePath:      filePath,
				})
			}
		}
	}
	return matches
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
	for _, m := range matches {
		fmt.Printf("%s Matched: %s, Regex: %s, Begin: %d, End: %d\n", m.FilePath, m.MatchedString, m.MatchedRegex, m.PosBegin, m.PosEnd)
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
