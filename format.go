package trame

import "github.com/citadellefr/trame/ot"

// Format reads a file into a document; key is the one the hub serves it
// under.
type Format func(key string, data []byte) (*ot.Tree, File, error)

// File is an open document: which edits it takes, and how it is written
// back into the kind of file it was read from.
type File interface {
	// Check refuses an edit the document cannot take, before it is rebased.
	Check(doc *ot.Tree, e ot.Edit, by Peer) error
	Encode(doc *ot.Tree) ([]byte, error)
}

// Follower is a File that follows an edit with changes of its own, applied
// to doc and answered, which every peer receives as the server's: the
// formulas of a workbook calculated again. since are the edits the edit was
// rebased over, by who made it.
type Follower interface {
	Follow(doc *ot.Tree, e ot.Edit, since []ot.Edit, by Peer) ot.Edit
}
