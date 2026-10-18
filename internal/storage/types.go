package storage

type Source struct {
	ID   int64
	Name string
}

type AddSourceResult struct {
	Source  Source
	Created bool
}

type WordContext struct {
	ID        int64
	MeaningID int64
	Source    Source
	Quote     string
}

type Word struct {
	ID   int64
	Text string
}

type Meaning struct {
	ID         int64
	Definition string
	Synonyms   []string
}

type AddMeaningResult struct {
	WordID         int64
	MeaningID      int64
	MeaningCreated bool
}
