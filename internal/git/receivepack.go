// Package git implements the git object model and the smart-HTTP protocol
// in pure Go (see pktline.go).
package git

import (
	"errors"
	"fmt"
	"io"
	"strings"
)

// pushCommand is one ref update from a receive-pack request.
type pushCommand struct {
	Old string
	New string
	Ref string
	// Caps holds the capabilities named on the first command line.
	Caps []string
}

// parsePushCommand parses one "<old> <new> <ref>[\x00caps]\n" line.
func parsePushCommand(line string) (pushCommand, error) {
	line = strings.TrimSuffix(line, "\n")
	var caps []string
	if nul := strings.IndexByte(line, 0); nul >= 0 {
		caps = strings.Split(line[nul+1:], " ")
		line = line[:nul]
	}
	parts := strings.SplitN(line, " ", 3)
	if len(parts) != 3 {
		return pushCommand{}, fmt.Errorf("git: bad push command %q", line)
	}
	if !IsOID(parts[0]) || !IsOID(parts[1]) {
		return pushCommand{}, fmt.Errorf("git: push command has a bad object id")
	}
	if !strings.HasPrefix(parts[2], "refs/") {
		return pushCommand{}, fmt.Errorf("git: push command names a bad ref %q", parts[2])
	}
	return pushCommand{Old: parts[0], New: parts[1], Ref: parts[2], Caps: caps}, nil
}

// writeReportLine writes one receive-pack report line as an inner pkt-line
// wrapped in an outer side-band band-1 pkt-line.
func writeReportLine(w io.Writer, line string) error {
	payload := append([]byte{1}, EncodeString(line)...)
	_, err := w.Write(EncodeLine(payload))
	return err
}

// writeReportFlush ends the report's inner pkt-line stream: a flush packet
// carried as side-band band-1 data. Without it, the client's packet reader
// hits end-of-stream while reading the report and dies with "the remote
// end hung up unexpectedly". The outer flush that follows ends the
// side-band stream itself.
func writeReportFlush(w io.Writer) error {
	payload := append([]byte{1}, []byte("0000")...)
	_, err := w.Write(EncodeLine(payload))
	return err
}

// ServeReceivePack handles the body of a POST /git-receive-pack (protocol
// v0). It stores every object the pack holds, then applies the ref updates
// that pass the fast-forward rule, and writes the report-status report.
// A push that mixes a fast-forward ref with a refused one applies the
// fast-forward ref and answers one report line per ref.
func ServeReceivePack(w io.Writer, repo *Repo, body []byte) error {
	// The command lines are pkt-lines up to the first flush; everything
	// after it is the pack. A delete-only push has no pack at all.
	var commands []pushCommand
	pos := 0
	for {
		if pos >= len(body) {
			return fmt.Errorf("git: receive-pack request has no flush packet")
		}
		pkt, n, err := decodeOne(body[pos:])
		if err != nil {
			return fmt.Errorf("git: receive-pack commands: %w", err)
		}
		pos += n
		if pkt.Kind == PacketFlush {
			break
		}
		if pkt.Kind != PacketData {
			continue
		}
		cmd, err := parsePushCommand(string(pkt.Payload))
		if err != nil {
			return err
		}
		commands = append(commands, cmd)
	}
	packBytes := body[pos:]
	if len(packBytes) > 0 {
		// A thin pack may name a base the repository already holds.
		pack, err := ReadPack(packBytes, repo.ReadObject)
		if err != nil {
			// Unpack failed: the report carries only the unpack error,
			// and no ref moves.
			reason := strings.ReplaceAll(err.Error(), "\n", " ")
			if len(reason) > 200 {
				reason = reason[:200]
			}
			if werr := writeReportLine(w, "unpack "+reason+"\n"); werr != nil {
				return werr
			}
			if werr := writeReportFlush(w); werr != nil {
				return werr
			}
			return writeFlush(w)
		}
		// Store every object before any ref moves.
		for oid, obj := range pack.Objects {
			if err := repo.WriteObjectWithOID(oid, obj.Type, obj.Data); err != nil {
				return err
			}
		}
	}
	// The unpack line is always first, even for a delete-only push with
	// no pack: git's own receive-pack reports "unpack ok" whenever the
	// unpack did not fail, and the client reads it before the per-ref
	// lines.
	if err := writeReportLine(w, "unpack ok\n"); err != nil {
		return err
	}
	// Apply the ref updates, one at a time, collecting a report line each.
	for _, cmd := range commands {
		line, err := repo.applyPush(cmd)
		if err != nil {
			return err
		}
		if err := writeReportLine(w, line); err != nil {
			return err
		}
	}
	// End the report's inner pkt-line stream, then the side-band stream.
	if err := writeReportFlush(w); err != nil {
		return err
	}
	return writeFlush(w)
}

// applyPush applies one ref update and returns its report line ("ok <ref>"
// or "ng <ref> <reason>"). The fast-forward rule: a create (old is zero)
// needs the ref to be absent; a delete (new is zero) needs old to match the
// ref; an update needs the new tip to descend from the old one.
func (r *Repo) applyPush(cmd pushCommand) (string, error) {
	ng := func(reason string) (string, error) {
		return "ng " + cmd.Ref + " " + reason + "\n", nil
	}
	current, err := r.ReadRef(cmd.Ref)
	if err != nil {
		if IsNotFound(err) {
			current = ZeroOID
		} else {
			return "", err
		}
	}
	switch {
	case cmd.New == cmd.Old:
		// A no-op update: the ref already points where the client wants.
		return "ok " + cmd.Ref + "\n", nil
	case cmd.New == ZeroOID:
		// Delete.
		if current == ZeroOID {
			return ng("cannot delete a ref that does not exist")
		}
		if cmd.Old != current {
			return ng("non-fast-forward")
		}
		if err := r.DeleteRef(cmd.Ref); err != nil {
			return "", err
		}
		return "ok " + cmd.Ref + "\n", nil
	case cmd.Old == ZeroOID:
		// Create.
		if current != ZeroOID {
			return ng("non-fast-forward")
		}
		if !r.HasObject(cmd.New) {
			return ng("missing object")
		}
		if err := r.WriteRef(cmd.Ref, cmd.New); err != nil {
			return "", err
		}
		return "ok " + cmd.Ref + "\n", nil
	default:
		// Update: the client's old must match, and the new tip must
		// descend from it.
		if cmd.Old != current {
			return ng("non-fast-forward")
		}
		if !r.HasObject(cmd.New) {
			return ng("missing object")
		}
		ok, err := r.IsAncestor(cmd.Old, cmd.New)
		if err != nil {
			return "", err
		}
		if !ok {
			return ng("non-fast-forward")
		}
		if err := r.WriteRef(cmd.Ref, cmd.New); err != nil {
			return "", err
		}
		return "ok " + cmd.Ref + "\n", nil
	}
}

// IsNotFound reports whether an error is a not-found error (a missing ref
// or object), so callers can tell "absent" from a real failure.
func IsNotFound(err error) bool {
	return errors.Is(err, ErrRefNotFound) || errors.Is(err, ErrObjectNotFound)
}
