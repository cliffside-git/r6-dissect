package dissect

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

type roundFixture struct {
	players map[string][]byte
	order   []string
	updates []update
}

type update struct {
	kind, owner string
	packet      []byte
}

func loadRoundFixture(t *testing.T) roundFixture {
	t.Helper()
	f, err := os.Open("testdata/y11s3_round.hex")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	fx := roundFixture{players: map[string][]byte{}}
	s := bufio.NewScanner(f)
	s.Buffer(nil, 1<<20)
	for s.Scan() {
		fields := strings.Fields(s.Text())
		if len(fields) == 0 {
			continue
		}
		b, err := hex.DecodeString(fields[len(fields)-1])
		if err != nil {
			t.Fatal(err)
		}
		if fields[0] == "player" {
			fx.players[fields[1]] = b
			fx.order = append(fx.order, fields[1])
			continue
		}
		fx.updates = append(fx.updates, update{kind: fields[0], owner: fields[1], packet: b})
	}
	if err := s.Err(); err != nil {
		t.Fatal(err)
	}
	return fx
}

type headerPlayer struct {
	name string
	team int
}

// restOfRound stands in for the megabytes a real round carries after its last
// scoreboard update, so readers that skip ahead stay inside the buffer.
const restOfRound = 512

func newRound(t *testing.T, players []headerPlayer, body ...[]byte) *Reader {
	t.Helper()
	var b bytes.Buffer
	b.WriteString("dissect")
	b.Write(make([]byte, 14))
	prop := func(k, v string) {
		for _, s := range []string{k, v} {
			b.WriteByte(byte(len(s)))
			b.Write(make([]byte, 7))
			b.WriteString(s)
		}
	}
	for _, kv := range [][2]string{
		{"version", "Y11S3_Alpha04"}, {"code", "9984445"}, {"datetime", "2026-11-04-18-41-26"},
		{"matchtype", "2"}, {"worldid", "1"}, {"recordingplayerid", "0"}, {"gamemodeid", "327933806"},
		{"roundspermatch", "9"}, {"roundspermatchovertime", "3"}, {"roundnumber", "0"},
		{"overtimeroundnumber", "0"}, {"startingteamscore0", "0"}, {"startingteamscore1", "0"},
		{"id", "match"},
	} {
		prop(kv[0], kv[1])
	}
	for i, p := range players {
		prop("playerid", strconv.Itoa(i+1))
		prop("playername", p.name)
		prop("team", strconv.Itoa(p.team))
	}
	prop("teamscore0", "0")
	prop("teamscore1", "0")
	for _, part := range body {
		b.Write(part)
	}
	b.Write(make([]byte, restOfRound))

	enc, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewReader(bytes.NewReader(enc.EncodeAll(b.Bytes(), nil)))
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	return r
}

func readRound(t *testing.T, players []headerPlayer, body ...[]byte) *Reader {
	t.Helper()
	r := newRound(t, players, body...)
	if err := r.Read(); !Ok(err) {
		t.Fatalf("Read: %v", err)
	}
	return r
}

func playerStats(t *testing.T, r *Reader) (stats map[string]PlayerRoundStats) {
	t.Helper()
	defer func() {
		if p := recover(); p != nil {
			t.Fatalf("PlayerStats panicked: %v", p)
		}
	}()
	stats = map[string]PlayerRoundStats{}
	for _, s := range r.PlayerStats() {
		stats[s.Username] = s
	}
	return stats
}

func TestScoreboardUpdatesGoToTheComponentOwner(t *testing.T) {
	fx := loadRoundFixture(t)
	var body [][]byte
	for _, name := range fx.order {
		body = append(body, fx.players[name])
	}
	for _, u := range fx.updates {
		body = append(body, u.packet)
	}
	stats := playerStats(t, readRound(t, nil, body...))

	want := map[string]struct{ assists, score int }{
		"player01": {1, 370},
		"player02": {0, 930},
		"player03": {0, 392},
		"player04": {0, 0},
		"player05": {1, 0},
		"player06": {0, 500},
		"player07": {1, 0},
		"player08": {1, 0},
		"player09": {0, 0},
		"player10": {1, 0},
	}
	if len(stats) != len(want) {
		t.Fatalf("got stats for %d players, want %d", len(stats), len(want))
	}
	for name, w := range want {
		got := stats[name]
		if got.Assists != w.assists || got.Score != w.score {
			t.Errorf("%s: assists=%d score=%d, want assists=%d score=%d", name, got.Assists, got.Score, w.assists, w.score)
		}
	}
}

func TestShortRoundGivesPresentPlayersTheirStats(t *testing.T) {
	fx := loadRoundFixture(t)
	body := [][]byte{fx.players["player01"], fx.players["player07"]}
	for _, u := range fx.updates {
		if u.kind == "assists" && (u.owner == "player01" || u.owner == "player07") {
			body = append(body, u.packet)
		}
	}
	header := []headerPlayer{{"player01", 0}, {"player04", 0}, {"player07", 1}}
	r := readRound(t, header, body...)
	stats := playerStats(t, r)

	if len(stats) != 2 {
		t.Fatalf("got stats for %d players, want player01 and player07 only", len(stats))
	}
	for name, team := range map[string]int{"player01": 0, "player07": 1} {
		got, ok := stats[name]
		if !ok {
			t.Fatalf("no stats for %s", name)
		}
		if got.Assists != 1 {
			t.Errorf("%s: assists=%d, want 1", name, got.Assists)
		}
		if got.TeamIndex != team {
			t.Errorf("%s: team %d, want %d from the header", name, got.TeamIndex, team)
		}
	}
	if r.Header.Teams[0].Role != Attack || r.Header.Teams[1].Role != Defense {
		t.Errorf("team roles = %q/%q, want Attack/Defense from player01's attacker", r.Header.Teams[0].Role, r.Header.Teams[1].Role)
	}
}

func TestHeaderKeepsItsLastPlayer(t *testing.T) {
	r := newRound(t, []headerPlayer{{"player01", 0}, {"player07", 1}})
	if len(r.Header.Players) != 2 {
		t.Fatalf("header has %d players, want 2", len(r.Header.Players))
	}
	if p := r.Header.Players[1]; p.Username != "player07" || p.TeamIndex != 1 {
		t.Errorf("last header player = %s on team %d, want player07 on team 1", p.Username, p.TeamIndex)
	}
}

func TestPlayerStatsSkipsFeedNamesOutsideTheRound(t *testing.T) {
	no := false
	r := &Reader{}
	r.Header.Players = []Player{{Username: "player01"}, {Username: "player07", TeamIndex: 1}}
	r.MatchFeedback = []MatchUpdate{
		{Type: Kill, Username: "outsider", Target: "player07", Headshot: &no},
		{Type: Kill, Username: "player07", Target: "outsider", Headshot: &no},
	}
	stats := playerStats(t, r)

	if got := stats["player01"]; got.Kills != 0 || got.Died {
		t.Errorf("player01: kills=%d died=%v, want 0 and alive", got.Kills, got.Died)
	}
	if got := stats["player07"]; got.Kills != 1 || !got.Died {
		t.Errorf("player07: kills=%d died=%v, want 1 kill and dead", got.Kills, got.Died)
	}
}
