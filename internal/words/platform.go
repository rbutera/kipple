package words

import "github.com/rbutera/kipple/internal/output"

// Phrases are the platform phrases (output.Phrases): what the user's Trash
// is called and how they put things back without kipple.
type Phrases = output.Phrases

// PhrasesFor returns the phrases for goos.
func PhrasesFor(goos string) Phrases { return output.PhrasesFor(goos) }

// Here are the phrases for the platform kipple runs on.
var Here = output.Here
