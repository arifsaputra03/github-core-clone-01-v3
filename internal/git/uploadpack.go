// Package git implements the git object model and the smart-HTTP protocol
// in pure Go (see pktline.go).
package git

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// The capabilities this forge advertises. They are what the suite's own
// client (the ground truth for the wire) requires.
//
//   - v2 upload-pack: ls-refs=unborn, fetch=shallow wait-for-done,
//     object-format=sha1, agent=forge/1.0
//   - v0 upload-pack: side-band-64k, thin-pack, ofs-delta, shallow,
//     symref=HEAD:refs/heads/main, object-format=sha1, agent=forge/1.0
//   - v0 receive-pack: report-status, side-band-64k, delete-refs,
//     ofs-delta, agent=forge/1.0
const (
	// DefaultBranch is the branch HEAD points at in every repository.
	DefaultBranch = "main"
	// Agent is the agent string advertised on the wire.
	Agent = "forge/1.0"
)

// v2UploadCaps are the capabilities advertised by the v2 upload-pack
// advertisement.
var v2UploadCaps = []string{
	"ls-refs=unborn",
	"fetch=shallow wait-for-done",
	"object-format=sha1",
	"agent=" + Agent,
}

// v0UploadCaps are the capabilities on the first line of the v0
// upload-pack advertisement.
var v0UploadCaps = []string{
	"side-band-64k",
	"thin-pack",
	"ofs-delta",
	"shallow",
	"symref=HEAD:refs/heads/" + DefaultBranch,
	"object-format=sha1",
	"agent=" + Agent,
}

// v0ReceiveCaps are the capabilities on the first line of the v0
// receive-pack advertisement.
var v0ReceiveCaps = []string{
	"report-status",
	"side-band-64k",
	"delete-refs",
	"ofs-delta",
	"agent=" + Agent,
}

// writeLine writes one pkt-line carrying s.
func writeLine(w io.Writer, s string) error {
	_, err := w.Write(EncodeString(s))
	return err
}

// writeFlush writes a flush packet.
func writeFlush(w io.Writer) error {
	_, err := w.Write(FlushPacket())
	return err
}

// writeDelim writes a delimiter packet (0001). In protocol v2, sections
// of a response are separated by delimiter packets, not by flush packets:
// a flush packet ends the whole response. (See gitprotocol-v2: "output =
// [acknowledgments delim-pkt] [shallow-info delim-pkt] packfile
// flush-pkt".)
func writeDelim(w io.Writer) error {
	_, err := w.Write(DelimPacket())
	return err
}

// pktItem is one element of a parsed pkt-line stream: a data line, or a
// flush/delim/response-end marker.
type pktItem struct {
	kind pktKind
	text string
}

type pktKind int

const (
	pktLine pktKind = iota
	pktFlush
	pktDelim
	pktResponseEnd
)

// parsePktLines splits a request body into its pkt-line items.
func parsePktLines(data []byte) ([]pktItem, error) {
	packets, err := DecodePackets(data)
	if err != nil {
		return nil, err
	}
	items := make([]pktItem, 0, len(packets))
	for _, p := range packets {
		switch p.Kind {
		case PacketFlush:
			items = append(items, pktItem{kind: pktFlush})
		case PacketDelim:
			items = append(items, pktItem{kind: pktDelim})
		case PacketResponseEnd:
			items = append(items, pktItem{kind: pktResponseEnd})
		default:
			items = append(items, pktItem{kind: pktLine, text: string(p.Payload)})
		}
	}
	return items, nil
}

// writeServicePreamble writes the "# service=... " line and a flush packet
// that open a v0 advertisement.
func writeServicePreamble(w io.Writer, service string) error {
	if err := writeLine(w, "# service="+service+"\n"); err != nil {
		return err
	}
	return writeFlush(w)
}

// AdvertiseUploadPackV2 writes the protocol-v2 upload-pack advertisement:
// the service preamble, a flush, "version 2", the capability lines, a flush.
func AdvertiseUploadPackV2(w io.Writer) error {
	if err := writeServicePreamble(w, "git-upload-pack"); err != nil {
		return err
	}
	if err := writeLine(w, "version 2\n"); err != nil {
		return err
	}
	for _, cap := range v2UploadCaps {
		if err := writeLine(w, cap+"\n"); err != nil {
			return err
		}
	}
	return writeFlush(w)
}

// AdvertiseUploadPackV0 writes the protocol-v0 upload-pack advertisement:
// the service preamble, a flush, then one line per ref (HEAD first) with the
// capabilities on the first line, then a flush. An empty repository
// advertises the all-zero capabilities^{} line.
func AdvertiseUploadPackV0(w io.Writer, repo *Repo) error {
	if err := writeServicePreamble(w, "git-upload-pack"); err != nil {
		return err
	}
	refs, err := repo.ListRefs()
	if err != nil {
		return err
	}
	caps := strings.Join(v0UploadCaps, " ")
	if len(refs) == 0 {
		if err := writeLine(w, ZeroOID+" capabilities^{}\x00"+caps+"\n"); err != nil {
			return err
		}
		return writeFlush(w)
	}
	headOID, err := repo.HeadOID()
	if err != nil {
		return err
	}
	first := true
	writeRefLine := func(oid, name string) error {
		line := oid + " " + name
		if first {
			line += "\x00" + caps
			first = false
		}
		return writeLine(w, line+"\n")
	}
	if headOID != ZeroOID {
		if err := writeRefLine(headOID, "HEAD"); err != nil {
			return err
		}
	}
	for _, ref := range refs {
		if err := writeRefLine(ref.OID, ref.Name); err != nil {
			return err
		}
	}
	return writeFlush(w)
}

// AdvertiseReceivePackV0 writes the protocol-v0 receive-pack
// advertisement, in the same shape as the v0 upload-pack one.
func AdvertiseReceivePackV0(w io.Writer, repo *Repo) error {
	if err := writeServicePreamble(w, "git-receive-pack"); err != nil {
		return err
	}
	refs, err := repo.ListRefs()
	if err != nil {
		return err
	}
	caps := strings.Join(v0ReceiveCaps, " ")
	if len(refs) == 0 {
		if err := writeLine(w, ZeroOID+" capabilities^{}\x00"+caps+"\n"); err != nil {
			return err
		}
		return writeFlush(w)
	}
	headOID, err := repo.HeadOID()
	if err != nil {
		return err
	}
	first := true
	writeRefLine := func(oid, name string) error {
		line := oid + " " + name
		if first {
			line += "\x00" + caps
			first = false
		}
		return writeLine(w, line+"\n")
	}
	if headOID != ZeroOID {
		if err := writeRefLine(headOID, "HEAD"); err != nil {
			return err
		}
	}
	for _, ref := range refs {
		if err := writeRefLine(ref.OID, ref.Name); err != nil {
			return err
		}
	}
	return writeFlush(w)
}

// lsRefsArgs is what a v2 ls-refs request asks for.
type lsRefsArgs struct {
	peel      bool
	symrefs   bool
	unborn    bool
	refPrefix []string
}

// parseLsRefsArgs reads the arguments after the delim of a v2 ls-refs
// request.
func parseLsRefsArgs(items []pktItem) lsRefsArgs {
	args := lsRefsArgs{}
	inArgs := false
	for _, it := range items {
		if it.kind == pktDelim {
			inArgs = true
			continue
		}
		if !inArgs || it.kind != pktLine {
			continue
		}
		arg := strings.TrimSuffix(it.text, "\n")
		switch {
		case arg == "peel":
			args.peel = true
		case arg == "symrefs":
			args.symrefs = true
		case arg == "unborn":
			args.unborn = true
		case strings.HasPrefix(arg, "ref-prefix "):
			args.refPrefix = append(args.refPrefix, strings.TrimPrefix(arg, "ref-prefix "))
		}
	}
	return args
}

// ServeLsRefs answers a v2 ls-refs request. HEAD is listed (with
// symref-target) only when symrefs was asked and no ref-prefixes were given
// (HEAD matches no prefix). Annotated tags are peeled when peel was asked.
func ServeLsRefs(w io.Writer, repo *Repo, args lsRefsArgs) error {
	refs, err := repo.ListRefs()
	if err != nil {
		return err
	}
	headTarget, err := repo.HeadTarget()
	if err != nil {
		return err
	}
	headOID, err := repo.HeadOID()
	if err != nil {
		return err
	}
	matches := func(name string) bool {
		if len(args.refPrefix) == 0 {
			return true
		}
		for _, p := range args.refPrefix {
			if strings.HasPrefix(name, p) {
				return true
			}
		}
		return false
	}
	if args.symrefs && matches("HEAD") {
		if headOID == ZeroOID {
			if args.unborn {
				if err := writeLine(w, "unborn HEAD symref-target:"+headTarget+"\n"); err != nil {
					return err
				}
			}
		} else {
			if err := writeLine(w, headOID+" HEAD symref-target:"+headTarget+"\n"); err != nil {
				return err
			}
		}
	}
	for _, ref := range refs {
		if !matches(ref.Name) {
			continue
		}
		line := ref.OID + " " + ref.Name
		if args.peel {
			peeled, err := repo.PeelTag(ref.OID)
			if err == nil && peeled != ref.OID {
				line += " peeled:" + peeled
			}
		}
		if err := writeLine(w, line+"\n"); err != nil {
			return err
		}
	}
	return writeFlush(w)
}

// fetchArgs is what a v2 fetch request asks for.
type fetchArgs struct {
	wants          []string
	haves          []string
	shallow        []string // the client's existing shallow boundaries
	deepen         int      // 0 means no depth limit
	deepenRelative bool     // deepen is relative to the client's current depth
	thinPack       bool
	ofsDelta       bool
}

// parseFetchArgs reads the arguments after the delim of a v2 fetch request.
func parseFetchArgs(items []pktItem) fetchArgs {
	args := fetchArgs{}
	inArgs := false
	for _, it := range items {
		if it.kind == pktDelim {
			inArgs = true
			continue
		}
		if !inArgs || it.kind != pktLine {
			continue
		}
		arg := strings.TrimSuffix(it.text, "\n")
		switch {
		case arg == "thin-pack":
			args.thinPack = true
		case arg == "ofs-delta":
			args.ofsDelta = true
		case strings.HasPrefix(arg, "want "):
			args.wants = append(args.wants, strings.TrimPrefix(arg, "want "))
		case strings.HasPrefix(arg, "have "):
			args.haves = append(args.haves, strings.TrimPrefix(arg, "have "))
		case strings.HasPrefix(arg, "shallow "):
			args.shallow = append(args.shallow, strings.TrimPrefix(arg, "shallow "))
		case strings.HasPrefix(arg, "deepen "):
			fmt.Sscanf(strings.TrimPrefix(arg, "deepen "), "%d", &args.deepen)
		case arg == "deepen-relative":
			args.deepenRelative = true
		}
	}
	return args
}

// writeSideBandPack writes a packfile as side-band band-1 packets, each
// holding at most 65515 data bytes (so the pkt-line stays within the 65520
// limit). At least one packet is always written, even for an empty pack.
func writeSideBandPack(w io.Writer, pack []byte) error {
	for {
		n := len(pack)
		if n > 65515 {
			n = 65515
		}
		chunk := pack[:n]
		pack = pack[n:]
		payload := make([]byte, 0, len(chunk)+1)
		payload = append(payload, 1) // band 1: pack data
		payload = append(payload, chunk...)
		if _, err := w.Write(EncodeLine(payload)); err != nil {
			return err
		}
		if len(pack) == 0 {
			break
		}
	}
	return nil
}

// computeFetchPack walks from the wants (depth limited when deepen was
// asked), removes the objects the client already holds (its haves, walked
// without crossing its shallow boundaries), and returns the pack bytes plus
// the shallow-info for the response.
func computeFetchPack(repo *Repo, args fetchArgs) (pack []byte, shallow, unshallow []string, err error) {
	if len(args.wants) == 0 {
		return nil, nil, nil, fmt.Errorf("git: fetch without a want")
	}
	for _, want := range args.wants {
		if !repo.HasObject(want) {
			return nil, nil, nil, fmt.Errorf("git: fetch wants unknown object %s", want)
		}
	}
	// The set the client already holds: its haves, walked without crossing
	// its shallow boundaries (a shallow client holds only the boundary
	// commit, not its history).
	haveSet := map[string]bool{}
	if len(args.haves) > 0 {
		stopAt := map[string]bool{}
		for _, s := range args.shallow {
			stopAt[s] = true
		}
		haveWalk, err := repo.WalkReachable(args.haves, stopAt)
		if err != nil {
			return nil, nil, nil, err
		}
		for oid := range haveWalk.Objects {
			haveSet[oid] = true
		}
	}
	// The set the client wants.
	var wantWalk *WalkResult
	if args.deepen > 0 {
		depth := args.deepen
		if args.deepenRelative {
			// The deepen value is relative to the client's current
			// depth: the current depth is the generation of its
			// shallow boundaries (from the wants) plus one, and the
			// new depth is that plus the relative deepen.
			maxGen := -1
			for _, b := range args.shallow {
				gen, err := repo.Generation(args.wants, b)
				if err != nil {
					return nil, nil, nil, err
				}
				if gen > maxGen {
					maxGen = gen
				}
			}
			if maxGen < 0 {
				// No boundary is reachable from the wants: treat the
				// deepen as absolute.
				depth = args.deepen
			} else {
				depth = maxGen + 1 + args.deepen
			}
		}
		wantWalk, err = repo.WalkDepth(args.wants, depth)
		if err != nil {
			return nil, nil, nil, err
		}
	} else {
		wantWalk, err = repo.WalkReachable(args.wants, nil)
		if err != nil {
			return nil, nil, nil, err
		}
	}
	// The pack is what the client wants minus what it already holds.
	packObjects := map[string]*Object{}
	for oid, obj := range wantWalk.Objects {
		if !haveSet[oid] {
			packObjects[oid] = obj
		}
	}
	pack, err = WritePack(SortObjects(packObjects))
	if err != nil {
		return nil, nil, nil, err
	}
	// Shallow-info, when a depth was asked: the new boundary commits, and
	// the old boundaries that are now fully present (unshallow).
	if args.deepen > 0 {
		shallow = wantWalk.Shallow
		newBoundary := map[string]bool{}
		for _, s := range shallow {
			newBoundary[s] = true
		}
		for _, old := range args.shallow {
			if _, inWant := wantWalk.Objects[old]; inWant && !newBoundary[old] {
				unshallow = append(unshallow, old)
			}
		}
	}
	return pack, shallow, unshallow, nil
}

// ServeFetchV2 answers a v2 fetch request: the acknowledgments section
// (when the client sent haves), the shallow-info section (when a depth was
// asked), then the packfile section (the last one, ending with the body).
func ServeFetchV2(w io.Writer, repo *Repo, args fetchArgs) error {
	pack, shallow, unshallow, err := computeFetchPack(repo, args)
	if err != nil {
		return err
	}
	if len(args.haves) > 0 {
		if err := writeLine(w, "acknowledgments\n"); err != nil {
			return err
		}
		// Acknowledge only the most recent have the server holds (the
		// first have the client sent that is present here): that is the
		// common commit the fetch builds on. The client did not ask for
		// multi_ack, so one ACK is what it expects before "ready".
		acked := ""
		for _, have := range args.haves {
			if repo.HasObject(have) {
				acked = have
				break
			}
		}
		if acked != "" {
			if err := writeLine(w, "ACK "+acked+"\n"); err != nil {
				return err
			}
			if err := writeLine(w, "ready\n"); err != nil {
				return err
			}
		} else {
			if err := writeLine(w, "NAK\n"); err != nil {
				return err
			}
		}
		// A delimiter separates this section from the next one; a flush
		// would end the whole response here.
		if err := writeDelim(w); err != nil {
			return err
		}
	}
	if args.deepen > 0 {
		if err := writeLine(w, "shallow-info\n"); err != nil {
			return err
		}
		for _, s := range shallow {
			if err := writeLine(w, "shallow "+s+"\n"); err != nil {
				return err
			}
		}
		for _, u := range unshallow {
			if err := writeLine(w, "unshallow "+u+"\n"); err != nil {
				return err
			}
		}
		if err := writeDelim(w); err != nil {
			return err
		}
	}
	if err := writeLine(w, "packfile\n"); err != nil {
		return err
	}
	if err := writeSideBandPack(w, pack); err != nil {
		return err
	}
	// The packfile section is the last one. A flush packet ends the
	// side-band stream, so the client's packet reader stops cleanly
	// instead of hitting end-of-stream while reading the pack.
	return writeFlush(w)
}

// ServeUploadPackV2 handles the body of a POST /git-upload-pack (protocol
// v2): it reads the command and dispatches to ls-refs or fetch.
func ServeUploadPackV2(w io.Writer, repo *Repo, body []byte) error {
	items, err := parsePktLines(body)
	if err != nil {
		return err
	}
	command := ""
	for _, it := range items {
		if it.kind == pktLine && strings.HasPrefix(it.text, "command=") {
			command = strings.TrimSuffix(strings.TrimPrefix(it.text, "command="), "\n")
			break
		}
	}
	switch command {
	case "ls-refs":
		return ServeLsRefs(w, repo, parseLsRefsArgs(items))
	case "fetch":
		return ServeFetchV2(w, repo, parseFetchArgs(items))
	default:
		return fmt.Errorf("git: unknown upload-pack command %q", command)
	}
}

// ServeUploadPackV0 handles the body of a POST /git-upload-pack when the
// client speaks protocol v0 (want/have/done). It is the fallback real git
// uses when v2 is unavailable; the suite speaks v2. The response is framed
// the v0 way: plain pkt-lines for ACK/NAK/shallow/unshallow, then the pack
// as side-band band-1 packets.
func ServeUploadPackV0(w io.Writer, repo *Repo, body []byte) error {
	items, err := parsePktLines(body)
	if err != nil {
		return err
	}
	var wants, haves, shallow []string
	deepen := 0
	for _, it := range items {
		if it.kind != pktLine {
			continue
		}
		arg := strings.TrimSuffix(it.text, "\n")
		// In v0 the first want line carries the capabilities after the
		// object id, separated by a space ("want <oid> <caps>").
		// Subsequent want lines are just "want <oid>".
		switch {
		case strings.HasPrefix(arg, "want "):
			rest := strings.TrimPrefix(arg, "want ")
			if space := strings.IndexByte(rest, ' '); space >= 0 {
				rest = rest[:space]
			}
			wants = append(wants, rest)
		case strings.HasPrefix(arg, "have "):
			haves = append(haves, strings.TrimPrefix(arg, "have "))
		case strings.HasPrefix(arg, "shallow "):
			shallow = append(shallow, strings.TrimPrefix(arg, "shallow "))
		case strings.HasPrefix(arg, "deepen "):
			fmt.Sscanf(strings.TrimPrefix(arg, "deepen "), "%d", &deepen)
		}
	}
	args := fetchArgs{wants: wants, haves: haves, shallow: shallow, deepen: deepen}
	pack, newShallow, unshallow, err := computeFetchPack(repo, args)
	if err != nil {
		return err
	}
	if len(haves) > 0 {
		acked := false
		for _, have := range haves {
			if repo.HasObject(have) {
				if err := writeLine(w, "ACK "+have+"\n"); err != nil {
					return err
				}
				acked = true
			}
		}
		if acked {
			if err := writeLine(w, "ready\n"); err != nil {
				return err
			}
		} else {
			if err := writeLine(w, "NAK\n"); err != nil {
				return err
			}
		}
	} else {
		// No haves (a fresh clone): there is no common base, so the
		// server answers NAK before the pack.
		if err := writeLine(w, "NAK\n"); err != nil {
			return err
		}
	}
	if deepen > 0 {
		for _, s := range newShallow {
			if err := writeLine(w, "shallow "+s+"\n"); err != nil {
				return err
			}
		}
		for _, u := range unshallow {
			if err := writeLine(w, "unshallow "+u+"\n"); err != nil {
				return err
			}
		}
	}
	if err := writeSideBandPack(w, pack); err != nil {
		return err
	}
	// A flush ends the side-band stream, so the client's demultiplexer
	// stops cleanly instead of hitting end-of-stream mid-packet.
	return writeFlush(w)
}

// SortedRefNames returns the repository's ref names in sorted order (a
// convenience for callers that list refs).
func SortedRefNames(refs []Ref) []string {
	names := make([]string, len(refs))
	for i, r := range refs {
		names[i] = r.Name
	}
	sort.Strings(names)
	return names
}
