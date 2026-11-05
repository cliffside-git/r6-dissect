package dissect

import (
	"bytes"

	"github.com/rs/zerolog/log"
)

// Scoreboard holds one entry per player with a non-zero assist or score update
// this round, keyed by the player's DissectID.
type Scoreboard struct {
	Players []ScoreboardPlayer
}

type ScoreboardPlayer struct {
	ID               []byte
	Score            uint32
	Assists          uint32
	AssistsFromRound uint32
}

func (s *Scoreboard) find(id []byte) *ScoreboardPlayer {
	for i := range s.Players {
		if bytes.Equal(s.Players[i].ID, id) {
			return &s.Players[i]
		}
	}
	return nil
}

func (s *Scoreboard) entry(id []byte) *ScoreboardPlayer {
	if p := s.find(id); p != nil {
		return p
	}
	s.Players = append(s.Players, ScoreboardPlayer{ID: id})
	return &s.Players[len(s.Players)-1]
}

// Each player's counters live on a scoreboard component that the player's
// entity links just before its player packet (`EB 21 9B 38 <id>`). The link
// has sat 113 to 165 bytes before the packet from Y8S1 to Y11S3, and the next
// player's block starts kilobytes later.
var scoreboardLink = []byte{0xEB, 0x21, 0x9B, 0x38}

const scoreboardLinkWindow = 1024

func (r *Reader) playerScoreboardID(packet int) []byte {
	from := max(0, packet-scoreboardLinkWindow)
	i := bytes.LastIndex(r.b[from:packet], scoreboardLink)
	if i < 0 {
		return nil
	}
	i += from + len(scoreboardLink)
	return r.b[i : i+4]
}

// An update names its component as `23 <id:4> 00 00 00 00` right before the
// counter's marker. A property chained onto the previous one on the same
// component starts with `22` and names none; round-start snapshots are written
// that way.
func (r *Reader) scoreboardOwner(markerEnd int) int {
	marker := markerEnd - 4
	if marker < 9 || r.b[marker-9] != 0x23 {
		return -1
	}
	id := r.b[marker-8 : marker-4]
	for i, p := range r.Header.Players {
		if bytes.Equal(p.scoreboardID, id) {
			return i
		}
	}
	return -1
}

// this function fixes kills that were previously recorded as elims
func readScoreboardKills(r *Reader) error {
	idx := r.scoreboardOwner(r.offset)
	kills, err := r.Uint32()
	if err != nil {
		return err
	}
	if idx != -1 {
		username := r.Header.Players[idx].Username
		r.lastKillerFromScoreboard = username
		log.Warn().
			Str("username", username).
			Uint32("kills", kills).
			Msg("scoreboard_kill")
	}
	return nil
}

func readScoreboardAssists(r *Reader) error {
	idx := r.scoreboardOwner(r.offset)
	assists, err := r.Uint32()
	if err != nil {
		return err
	}
	if assists == 0 {
		return nil
	}
	username := "N/A"
	if idx != -1 {
		username = r.Header.Players[idx].Username
		p := r.Scoreboard.entry(r.Header.Players[idx].DissectID)
		p.Assists = assists
		p.AssistsFromRound++
	}
	log.Debug().
		Uint32("assists", assists).
		Str("username", username).
		Msg("scoreboard_assists")
	return nil
}

func readScoreboardScore(r *Reader) error {
	idx := r.scoreboardOwner(r.offset)
	score, err := r.Uint32()
	if err != nil {
		return err
	}
	if score == 0 {
		return nil
	}
	username := "N/A"
	if idx != -1 {
		username = r.Header.Players[idx].Username
		r.Scoreboard.entry(r.Header.Players[idx].DissectID).Score = score
	}
	log.Debug().
		Uint32("score", score).
		Str("username", username).
		Msg("scoreboard_score")
	return nil
}
