// Command readctl is frostread's CLI, over readd's private socket
// (docs/specs/api.md, readctl).
//
//	readctl [--socket PATH] COMMAND [ARGS]
//
// The socket is --socket, or READ_SOCKET, or /run/frostread/readctl.sock.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/frostyard/frostread/api"
)

// DefaultSocket is readd's private socket, as its service unit names it.
const DefaultSocket = "/run/frostread/readctl.sock"

// timeout bounds one command, so a stuck daemon does not hang the shell.
const timeout = 2 * time.Minute

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(os.Stderr, "readctl:", err)
			os.Exit(1)
		}
	}
}

const usage = `usage: readctl [--socket PATH] COMMAND [ARGS]

COMMAND:
  save <url>          save a link to read later
  add <feed-url>      subscribe to a feed, or to the feed a page names
  sources             list the sources and their unread counts
  import <file.opml>  import an OPML file, or - for stdin
  export              write the OPML to stdout
  poll                poll every source now
  search <query>      search titles, authors and article text
  notes-token         read the notes MCP token from stdin and set it
  status              print the version and whether save-as-note is ready

The socket is --socket, or READ_SOCKET, or ` + DefaultSocket + `.`

// run does one command.
func run(ctx context.Context, args []string, in io.Reader, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("readctl", flag.ContinueOnError)
	fs.SetOutput(errOut)
	socket := fs.String("socket", "", "readd's private socket")
	fs.Usage = func() { fmt.Fprintln(errOut, usage) }
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *socket == "" {
		*socket = os.Getenv("READ_SOCKET")
	}
	if *socket == "" {
		*socket = DefaultSocket
	}
	rest := fs.Args()
	if len(rest) == 0 {
		fmt.Fprintln(errOut, usage)
		return errors.New("no command")
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	c, _, err := api.Dial(ctx, *socket, "readctl")
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	read := c.Read()
	command, args := rest[0], rest[1:]
	switch command {
	case "save":
		if len(args) != 1 {
			return errors.New("usage: readctl save <url>")
		}
		it, err := read.SaveURL(ctx, args[0])
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "saved %d %s\n", it.ID, it.Title)
		return nil
	case "add":
		if len(args) != 1 {
			return errors.New("usage: readctl add <feed-url>")
		}
		src, err := read.AddSource(ctx, args[0])
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "added %d %s <%s>\n", src.ID, src.Title, src.URL)
		return nil
	case "sources":
		sources, err := read.Sources(ctx)
		if err != nil {
			return err
		}
		if len(sources) == 0 {
			fmt.Fprintln(out, "no sources")
			return nil
		}
		for _, s := range sources {
			fmt.Fprintf(out, "%d\t%d unread\t%s\t%s\n", s.ID, s.Unread, s.Title, s.URL)
			if s.LastError != "" {
				fmt.Fprintf(out, "\tlast error: %s\n", s.LastError)
			}
		}
		return nil
	case "import":
		if len(args) != 1 {
			return errors.New("usage: readctl import <file.opml> | -")
		}
		var data []byte
		var err error
		if args[0] == "-" {
			data, err = io.ReadAll(in)
		} else {
			data, err = os.ReadFile(args[0])
		}
		if err != nil {
			return err
		}
		added, skipped, err := read.ImportOPML(ctx, string(data))
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "added %d, skipped %d\n", added, skipped)
		return nil
	case "export":
		document, err := read.ExportOPML(ctx)
		if err != nil {
			return err
		}
		_, err = io.WriteString(out, document)
		return err
	case "poll":
		if len(args) > 1 {
			return errors.New("usage: readctl poll [source-id]")
		}
		if len(args) == 1 {
			var id int64
			if _, err := fmt.Sscan(args[0], &id); err != nil || id <= 0 {
				return errors.New("poll needs a source id")
			}
			if err := read.Refresh(ctx, &id); err != nil {
				return err
			}
			fmt.Fprintf(out, "polled %d\n", id)
			return nil
		}
		if err := read.Refresh(ctx, nil); err != nil {
			return err
		}
		fmt.Fprintln(out, "polled every source")
		return nil
	case "search":
		if len(args) == 0 {
			return errors.New("usage: readctl search <query>")
		}
		page, err := read.Search(ctx, strings.Join(args, " "), 50)
		if err != nil {
			return err
		}
		for _, it := range page.Items {
			fmt.Fprintf(out, "%d\t%s\n", it.ID, it.Title)
			if it.Snippet != "" {
				fmt.Fprintf(out, "\t%s\n", it.Snippet)
			}
		}
		if len(page.Items) == 0 {
			fmt.Fprintln(out, "no matches")
		}
		return nil
	case "notes-token":
		tok, err := io.ReadAll(io.LimitReader(in, 4<<10))
		if err != nil {
			return err
		}
		if err := read.SetNotesToken(ctx, strings.TrimSpace(string(tok))); err != nil {
			return err
		}
		fmt.Fprintln(out, "the notes token is set")
		return nil
	case "status":
		st, err := read.Status(ctx)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "readd %s\n", st.Version)
		if st.Notes.Configured {
			fmt.Fprintf(out, "save as a note: yes, vault %d, folder %s\n", st.Notes.VaultID, st.Notes.Folder)
		} else {
			fmt.Fprintln(out, "save as a note: not configured")
		}
		return nil
	default:
		fmt.Fprintln(errOut, usage)
		return fmt.Errorf("unknown command %q", command)
	}
}
