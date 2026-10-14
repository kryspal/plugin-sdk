// Package hanconv converts text between Simplified and Traditional Chinese with the dictionaries of OpenCC
// (Open Chinese Convert, Apache License 2.0; see NOTICE).
//
// It replaces github.com/liuzl/gocc, whose dependency chain links github.com/liuzl/cedar-go (GPL-2.0), and
// reproduces gocc's output exactly (the golden files in testdata/ were generated with gocc):
//
//   - A conversion is a chain of steps applied in order; the output of one step is the input of the next.
//   - Each step is a group of dictionaries. At every position the first dictionary of the group that has any
//     entry that is a prefix of the remaining text wins, and its longest such entry is replaced by the entry's
//     first value. Text that no dictionary matches is copied one character at a time.
//   - When a dictionary lists a key twice, the later line wins.
//
// The dictionaries are embedded (gzip-compressed) and loaded on first use. A Converter is safe for concurrent use.
package hanconv

import (
	"bufio"
	"compress/gzip"
	"embed"
	"fmt"
	"io"
	"strings"
	"sync"
	"unicode/utf8"
)

//go:embed dictionary/*.txt.gz
var dictionaries embed.FS

// chains lists, for every supported conversion, its steps and the dictionaries of each step (OpenCC's
// configuration files, as bundled with gocc).
var chains = map[string][][]string{
	"s2t":   {{"STPhrases", "STCharacters"}},
	"t2s":   {{"TSPhrases", "TSCharacters"}},
	"s2tw":  {{"STPhrases", "STCharacters"}, {"TWVariants"}},
	"tw2s":  {{"TWVariantsRevPhrases", "TWVariantsRev"}, {"TSPhrases", "TSCharacters"}},
	"s2twp": {{"STPhrases", "STCharacters"}, {"TWPhrases"}, {"TWVariants"}},
	"tw2sp": {{"TWVariantsRevPhrases", "TWVariantsRev"}, {"TWPhrasesRev"}, {"TSPhrases", "TSCharacters"}},
	"s2hk":  {{"STPhrases", "STCharacters"}, {"HKVariantsPhrases", "HKVariants"}},
	"hk2s":  {{"HKVariantsRevPhrases", "HKVariantsRev"}, {"TSPhrases", "TSCharacters"}},
	"t2tw":  {{"TWVariants"}},
	"t2hk":  {{"HKVariants"}},
}

// Converter converts text with one conversion, such as "s2t".
type Converter struct {
	name  string
	steps [][]*dictionary
}

// New returns the converter for conversion: s2t, t2s, s2tw, tw2s, s2twp, tw2sp, s2hk, hk2s, t2tw or t2hk
// (the names of OpenCC's configurations). The dictionaries it needs are loaded on the first call that uses them.
func New(conversion string) (*Converter, error) {
	chain, ok := chains[conversion]
	if !ok {
		return nil, fmt.Errorf("hanconv: unknown conversion %q", conversion)
	}
	c := &Converter{name: conversion, steps: make([][]*dictionary, len(chain))}
	for i, step := range chain {
		for _, file := range step {
			d, err := load(file)
			if err != nil {
				return nil, err
			}
			c.steps[i] = append(c.steps[i], d)
		}
	}
	return c, nil
}

// Name returns the conversion, such as "s2t".
func (c *Converter) Name() string { return c.name }

// Convert returns in converted. An invalid UTF-8 byte that no dictionary matches becomes U+FFFD and the bytes
// after it are kept (gocc dropped up to two of them).
func (c *Converter) Convert(in string) string {
	for _, step := range c.steps {
		in = convertStep(step, in)
	}
	return in
}

func convertStep(group []*dictionary, in string) string {
	var out strings.Builder
	out.Grow(len(in))
	for i := 0; i < len(in); {
		matched := false
		for _, d := range group {
			if n, v, ok := d.longestPrefix(in[i:]); ok {
				out.WriteString(v)
				i += n
				matched = true
				break
			}
		}
		if !matched {
			r, size := utf8.DecodeRuneInString(in[i:])
			out.WriteRune(r)
			i += size
		}
	}
	return out.String()
}

// dictionary maps a key to its first value. maxKey is the length in bytes of the longest key.
type dictionary struct {
	entries map[string]string
	maxKey  int
}

// longestPrefix returns the longest key that is a prefix of s, its length in bytes and its value.
func (d *dictionary) longestPrefix(s string) (int, string, bool) {
	for n := min(d.maxKey, len(s)); n > 0; n-- {
		if n < len(s) && !utf8.RuneStart(s[n]) {
			continue
		}
		if v, ok := d.entries[s[:n]]; ok {
			return n, v, true
		}
	}
	return 0, "", false
}

var (
	loadMu sync.Mutex
	loaded = map[string]*dictionary{}
)

// load returns the parsed dictionary file (without the .txt.gz suffix), parsing it once.
func load(file string) (*dictionary, error) {
	loadMu.Lock()
	defer loadMu.Unlock()
	if d, ok := loaded[file]; ok {
		return d, nil
	}
	f, err := dictionaries.Open("dictionary/" + file + ".txt.gz")
	if err != nil {
		return nil, fmt.Errorf("hanconv: dictionary %s: %w", file, err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("hanconv: dictionary %s: %w", file, err)
	}
	d, err := parse(zr)
	if err != nil {
		return nil, fmt.Errorf("hanconv: dictionary %s: %w", file, err)
	}
	loaded[file] = d
	return d, nil
}

// parse reads OpenCC's text format: one entry per line, the key and its values separated by a tab, several
// values separated by spaces (or by further tabs). Lines without a value are skipped; a later line for the same
// key replaces the earlier one. Only the first value is kept.
func parse(r io.Reader) (*dictionary, error) {
	d := &dictionary{entries: map[string]string{}}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		items := strings.Split(strings.TrimSpace(sc.Text()), "\t")
		if len(items) < 2 {
			continue
		}
		value := items[1]
		if len(items) == 2 {
			fields := strings.Fields(items[1])
			if len(fields) == 0 {
				continue
			}
			value = fields[0]
		}
		d.entries[items[0]] = value
		d.maxKey = max(d.maxKey, len(items[0]))
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return d, nil
}
