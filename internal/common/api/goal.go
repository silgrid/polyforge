package api

// goal.go: the work-item goal size contract of the Core create face.

// MaxGoalRunes is the work-item goal size cap in CHARACTERS (runes)
// (polyforge#149 D3): the v1.5 "500 characters" contract is enforced by
// rune counting, so a 500-character CJK goal passes despite its byte
// length.
const MaxGoalRunes = 500
