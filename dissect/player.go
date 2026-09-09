package dissect

import (
	"bytes"
	"strings"

	"github.com/rs/zerolog/log"
)

// Player-id markers seen in the wild. Ubisoft does not move this forward
// monotonically: the classic marker was replaced by playerIDMidseason in build
// 9734089 (a Y11S2 mid-season patch) and then REVERTED in 9785623, which still
// reports itself as Y11S2. A `CodeVersion >= 9734089` gate therefore looked
// right for one build and silently broke every build after it.
var (
	playerIDClassic   = []byte{0x33, 0xD8, 0x3D, 0x4F, 0x23}
	playerIDMidseason = []byte{0x8C, 0x61, 0x1A, 0x75, 0x23}
	playerIDY7S2      = []byte{0xE6, 0xF9, 0x7D, 0x86}
)

// playerIDIndicator picks the player-id marker this replay actually uses, by
// looking for it, and caches the answer for the round.
//
// This is deliberately detection rather than a version gate. Seek returns
// io.EOF when its pattern is absent, Reader.Read aborts on the first listener
// error, and MatchReader.Read aborts on the first round -- so guessing wrong
// here does not degrade one field, it fails the entire match with
// "read round 1: EOF" and leaves an empty scoreboard for PlayerStats to panic
// on. That is exactly what happened between 2026-06-21 and 2026-09-09: ~100%
// of R6 parses failed for eleven weeks because a marker flipped back and the
// gate did not know. Detection cannot go stale; a boundary re-derived every
// season can, and did.
//
// Cost is one scan of the decompressed round on first use, amortised across
// all ten players.
func (r *Reader) playerIDIndicator() []byte {
	if r.idIndicator != nil {
		return r.idIndicator
	}
	switch {
	case r.Header.CodeVersion <= Y7S2:
		r.idIndicator = playerIDY7S2
	case bytes.Contains(r.b, playerIDClassic):
		r.idIndicator = playerIDClassic
	case bytes.Contains(r.b, playerIDMidseason):
		r.idIndicator = playerIDMidseason
	default:
		// Neither is present: a genuinely new format. Fall back to the classic
		// marker so the failure surfaces as the usual seek error rather than a
		// nil pattern, and say so -- this is the line that should send whoever
		// is on call to the runbook.
		log.Warn().Int("codeVersion", r.Header.CodeVersion).Str("season", r.Header.GameVersion).
			Msg("no known player-id indicator in this build; parsing will fail")
		r.idIndicator = playerIDClassic
	}
	return r.idIndicator
}

func readPlayer(r *Reader) error {
	idIndicator := r.playerIDIndicator()
	spawnIndicator := []byte{0xAF, 0x98, 0x99, 0xCA}
	profileIDIndicator := []byte{0x8A, 0x50, 0x9B, 0xD0}
	//unknownIndicator := []byte{0x22, 0xEE, 0xD4, 0x45, 0xC8, 0x08} // maybe player appearance?
	r.playersRead++
	defer func() {
		if r.playersRead == 10 {
			r.deriveTeamRoles()
		}
	}()
	username, err := r.String()
	if err != nil {
		return err
	}
	if r.Header.CodeVersion >= Y7S4 {
		if err := r.Seek([]byte{0x40, 0xF2, 0x15, 0x04}); err != nil {
			return err
		}
		if err = r.Skip(8); err != nil {
			return err
		}
		swap, err := r.Bytes(1)
		if err != nil {
			return err
		}
		// Sometimes, 0x40, 0xF2, 0x15, 0x04 is sent twice.
		// Does not seem to be linked to role swap.
		if swap[0] == 0x9D {
			return nil
		}
	} else {
		if err := r.Seek([]byte{0x22, 0xA9, 0x26, 0x0B, 0xE4}); err != nil {
			return err
		}
	}
	op, err := r.Uint64() // Op before atk role swaps
	if err != nil {
		return err
	}
	if op == 0 { // Empty player slot
		log.Debug().Msg("empty player slot?")
		return nil
	}
	validPlayer, err := r.Bytes(1)
	if err != nil {
		return err
	}
	if validPlayer[0] != 0x22 {
		log.Warn().Uint64("op", op).Msg("strange invalid player located")
		return nil
	}
	if err := r.Seek(idIndicator); err != nil {
		return err
	}
	id, err := r.Bytes(4)
	if err != nil {
		return err
	}
	if err := r.Seek(spawnIndicator); err != nil {
		return err
	}
	spawn, err := r.String()
	if err != nil {
		return err
	}
	if spawn == "" {
		if err = r.Skip(10); err != nil {
			return err
		}
		valid, err := r.Bytes(1)
		if err != nil {
			return err
		}
		if !bytes.Equal(valid, []byte{0x1B}) {
			return nil
		}
	}
	teamIndex := 0
	if r.playersRead > 5 {
		teamIndex = 1
	}
	// ui id (y9s3+?)
	// there seems to be more to this, but its a quick fix for atk op swaps for now
	var uiID uint64
	if r.Header.CodeVersion >= Y9S3 {
		save := r.offset
		if r.Seek([]byte{0x38, 0xDF, 0xEE, 0x88}) != nil {
			r.offset = save // indicator moved (Y11S2 patch); uiID is non-critical, skip
		} else if r.Skip(13) == nil {
			uiID, _ = r.Uint64()
		}
	}
	// Older versions of siege did not include profile ids
	profileID := ""
	var unknownId uint64
	if len(r.Header.RecordingProfileID) > 0 {
		save := r.offset
		if r.Seek(profileIDIndicator) != nil {
			r.offset = save // indicator moved (Y11S2 patch); profileID is non-critical, skip
		} else if profileID, err = r.String(); err == nil {
			if r.Skip(5) == nil {
				unknownId, _ = r.Uint64()
			}
		}
		err = nil
	} else {
		log.Debug().Str("warn", "profileID not found, skipping").Send()
	}
	p := Player{
		ID:        unknownId,
		ProfileID: profileID,
		Username:  username,
		TeamIndex: teamIndex,
		Operator:  Operator(op),
		Spawn:     spawn,
		DissectID: id,
		uiID:      uiID,
	}
	if p.Operator != Recruit && p.Operator.Role() == Defense {
		p.Spawn = r.Header.Site // We cannot detect the spawn here on defense
	}
	log.Debug().Str("username", username).
		Int("teamIndex", teamIndex).
		Interface("op", p.Operator).
		Str("profileID", profileID).
		Hex("DissectID", id).
		Uint64("ID", p.ID).
		Uint64("uiID", p.uiID).
		Str("spawn", spawn).Send()
	found := false
	for i, existing := range r.Header.Players {
		if existing.Username == p.Username ||
			(r.Header.CodeVersion < Y8S2 && existing.ID == p.ID && p.ID != 0) ||
			(r.Header.CodeVersion >= Y8S2 && bytes.Equal(existing.DissectID, p.DissectID)) ||
			(r.Header.CodeVersion <= Y7S2 && strings.HasPrefix(p.Username, existing.Username)) {
			r.Header.Players[i].ProfileID = p.ProfileID
			r.Header.Players[i].Username = p.Username
			r.Header.Players[i].Operator = p.Operator
			r.Header.Players[i].Spawn = p.Spawn
			r.Header.Players[i].DissectID = p.DissectID
			r.Header.Players[i].uiID = p.uiID
			found = true
			break
		}
	}
	if !found && len(username) > 0 {
		r.Header.Players = append(r.Header.Players, p)
	}
	return err
}

func readAtkOpSwap(r *Reader) error {
	op, err := r.Uint64()
	if err != nil {
		return err
	}
	o := Operator(op)
	// before Y9S3 caster view overhaul
	if r.Header.CodeVersion < Y9S3 {
		if err = r.Skip(5); err != nil {
			return err
		}
		id, err := r.Bytes(4)
		if err != nil {
			return err
		}
		i := r.PlayerIndexByID(id)
		log.Debug().Hex("id", id).Interface("op", op).Msg("atk_op_swap")
		if i > -1 {
			r.Header.Players[i].Operator = o
			u := MatchUpdate{
				Type:          OperatorSwap,
				Username:      r.Header.Players[i].Username,
				Time:          r.timeRaw,
				TimeInSeconds: r.time,
				Operator:      o,
			}
			r.MatchFeedback = append(r.MatchFeedback, u)
			log.Debug().Interface("match_update", u).Send()
		}
		return nil
	}
	// after Y9S3 caster view overhaul
	if err = r.Skip(402); err != nil {
		return err
	}
	// id shows up in player data and in op swaps afaik
	id, err := r.Uint64()
	if err != nil {
		return err
	}
	for i, p := range r.Header.Players {
		if p.uiID == id {
			r.Header.Players[i].Operator = o
			u := MatchUpdate{
				Type:          OperatorSwap,
				Username:      p.Username,
				Time:          r.timeRaw,
				TimeInSeconds: r.time,
				Operator:      o,
			}
			r.MatchFeedback = append(r.MatchFeedback, u)
			log.Debug().Interface("match_update", u).Send()
			break
		}
	}
	return nil
}
