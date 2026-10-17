package dictionary

type Entry struct {
	Word       string
	Meanings   []Meaning
	SourceURLs []string
	License    License
}

type Meaning struct {
	PartOfSpeech string
	Definitions  []Definition
	Synonyms     []string
	Antonyms     []string
}

type Definition struct {
	Text     string
	Example  string
	Synonyms []string
	Antonyms []string
}

type License struct {
	Name string
	URL  string
}
