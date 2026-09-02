// Copyright (c) 2026 Michael D Henderson.

package web

import (
	"errors"

	"github.com/mdhender/mmm/internal/storage"
)

// docBase is where the documentation can be read on the web. The repo-relative
// path is shown alongside every link, because a household that cannot open the
// database may also not be online, and the same file is in their checkout.
const docBase = "https://github.com/mdhender/mmm/blob/main/"

// Doc is a document the problem page sends the reader to.
type Doc struct {
	Title string
	Path  string
	URL   string
}

func doc(title, path string) Doc {
	return Doc{Title: title, Path: path, URL: docBase + path}
}

var (
	docUpgrade   = doc("How to upgrade the application", "docs/how-to/upgrade-the-application.md")
	docFirstFile = doc("How to create your first checkbook", "docs/how-to/create-your-first-checkbook.md")
	docRestore   = doc("How to restore a backup", "docs/how-to/restore-a-backup.md")
	docReport    = doc("How to report a problem", "docs/how-to/report-a-problem.md")
	docManual    = doc("User manual", "docs/references/user-manual.md")
)

// Problem describes why the register cannot be served.
//
// It was once rendered at every address by a mux of its own, and that is exactly
// what this program must not do: the household whose checkbook will not open is
// the household that most needs the restore list, and a page that answers every
// address has no address left to put one at. So a startup failure now builds the
// ordinary Server with no store and hands it this, and the no-checkbook page
// carries it -- which is the rule the rest of the package already followed.
type Problem struct {
	// Heading says what happened, in the reader's terms.
	Heading string

	// Detail is the underlying error, for a report or a second opinion.
	Detail string

	// Steps are what the reader can safely do, in the order to try them
	// (SPECIFICATION.md RG-4). Never empty.
	Steps []string

	// Docs are where to read more.
	Docs []Doc

	// Database is the file that could not be opened.
	Database string

	Version string
}

// DescribeOpenError turns a failure to open the database into something a
// household can act on.
//
// The mapping is by sentinel, not by message text, so a change to the wording of
// an underlying error cannot silently turn a specific page into a vague one.
// Anything unrecognized falls through to a general page that still says what to
// do next, because a reader facing an unknown failure needs the advice more, not
// less.
func DescribeOpenError(err error, database string) Problem {
	p := Problem{
		Detail:   err.Error(),
		Database: database,
	}

	switch {
	case errors.Is(err, storage.ErrDatabaseTooNew):
		p.Heading = "This checkbook was written by a newer version of the program"
		p.Steps = []string{
			"Use the newer version of the program with this file. It is the one that understands it.",
			"If you meant to go back to an older version, restore the backup you took before upgrading and open that copy instead.",
			"Do not keep using this file with this version. It has been left untouched, and that is the safe state.",
		}
		p.Docs = []Doc{docUpgrade, docRestore, docReport}

	case errors.Is(err, storage.ErrNotCheckbook):
		p.Heading = "That file is not a checkbook"
		p.Steps = []string{
			"Check the path after -db. It is a database, but one written by a different program, and it has not been altered.",
			"To start a new checkbook, point -db at a file that does not exist yet.",
		}
		p.Docs = []Doc{docFirstFile, docManual}

	case errors.Is(err, storage.ErrIsBackup):
		// The command opens a backup read-only rather than refusing it
		// (storage.OpenOrReadOnly), so a reader should never arrive here. The
		// page is kept because storage.Open still reports this on its own, and
		// a caller that asks for a backup read-write should be answered in
		// words rather than by whatever the catch-all would say.
		p.Heading = "That file is a backup"
		// Worded to hold on both pages this can reach. The startup problem page
		// is a dead end with no form on it, so a step that says "press Open
		// again" would be an instruction the reader cannot follow.
		p.Steps = []string{
			"A backup is only ever opened for reading, so something asked for this one in a way it cannot be given. Nothing was written: a backup opened for writing stops being the copy that was taken.",
			"Open your checkbook instead — the file you keep your records in, rather than one of the copies beside it. Backups are named checkbook-YYYYMMDD-HHMMSS.db, for the moment they were taken.",
			"To work from these records, restore it. Restoring copies the backup to a new file and brings the copy up to date, and the backup itself is left exactly as it is.",
		}
		p.Docs = []Doc{docRestore, docManual}

	case errors.Is(err, storage.ErrDatabaseTooOld):
		p.Heading = "That backup was written by an older version of the program"
		p.Steps = []string{
			"Restore it. Restoring copies the backup to a new file and brings the copy up to date, which is the one place doing so is safe; the backup itself is left exactly as it is.",
			"Reading it in place is not possible, and that is deliberate. Bringing this file up to date would rewrite it, and a backup that has been rewritten is no longer the backup you took.",
		}
		p.Docs = []Doc{docRestore, docUpgrade, docManual}

	case errors.Is(err, storage.ErrMissingFile):
		p.Heading = "There is no file there"
		p.Steps = []string{
			"Check the path. Read-only never creates a file, so a name that does not exist is reported rather than turned into an empty checkbook.",
			"To start a new checkbook instead, type a path that does not exist yet and leave the read-only box unticked.",
		}
		p.Docs = []Doc{docFirstFile, docManual}

	case errors.Is(err, storage.ErrMissingDirectory):
		p.Heading = "That folder does not exist"
		p.Steps = []string{
			"Create the folder yourself, then start the program again. The program creates the database file but never creates folders, so a mistyped path is reported rather than built.",
			"Or correct the path after -db.",
		}
		p.Docs = []Doc{docFirstFile, docManual}

	case errors.Is(err, storage.ErrSchemaVersion):
		p.Heading = "This checkbook's schema is not the one this version expects"
		p.Steps = []string{
			"Restore your most recent backup and open that copy.",
			"Keep the current file. Do not delete it: it is what shows what went wrong.",
			"Report the problem, including both version numbers from the message below.",
		}
		p.Docs = []Doc{docRestore, docReport, docUpgrade}

	default:
		p.Heading = "The checkbook could not be opened"
		p.Steps = []string{
			"Check that the path below is the file you meant, and that it is not open in another program.",
			"If the file is damaged, restore your most recent backup and open that copy instead.",
			"Report the problem, including the message below.",
		}
		p.Docs = []Doc{docRestore, docReport, docManual}
	}

	return p
}
