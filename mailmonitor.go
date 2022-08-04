package main

import (
	"encoding/base64"
	"errors"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	imap "github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
	"github.com/emersion/go-message/mail"
	"gopkg.in/yaml.v2"
)

const (
	layout string = "2006-01-02T15:04:05Z07:00"
)

type Monitor struct {
	IMAPUri           string `yaml:"IMAPUri"`
	Username          string `yaml:"Username"`
	Password          string `yaml:"Password"`
	SourceFolder      string `yaml:"SourceFolder"`
	DestinationFolder string `yaml:"DestinationFolder"`
	TLS               bool   `yaml:"TLS"`
	DebugLevel        int64  `yaml:"DebugLevel"`
	Client            *client.Client
	FetchSet          *imap.SeqSet
	ReadSet           *imap.SeqSet
	MessageChan       chan *imap.Message
	DoneChan          chan error
}

func NewMonitor(IMAPUri, Username, Password, SourceFolder, DestinationFolder string, TLS bool) (Monitor, error) {
	m := Monitor{
		IMAPUri:           IMAPUri,
		Username:          Username,
		Password:          Password,
		TLS:               TLS,
		FetchSet:          &imap.SeqSet{},
		ReadSet:           &imap.SeqSet{},
		SourceFolder:      SourceFolder,
		DestinationFolder: DestinationFolder,
		MessageChan:       make(chan *imap.Message, 10),
		DoneChan:          make(chan error),
	}
	if TLS {
		cl, err := client.DialTLS(m.IMAPUri, nil)
		if err != nil {
			return m, err
		}
		m.Client = cl
		return m, nil
	} else {
		cl, err := client.Dial(m.IMAPUri)
		if err != nil {
			return m, err
		}
		err = cl.StartTLS(
			nil,
		)

		if err != nil {
			return m, err
		}
		m.Client = cl
		return m, nil
	}
}

func NewMonitorFromFile(path string) (Monitor, error) {
	m := Monitor{
		FetchSet:    &imap.SeqSet{},
		ReadSet:     &imap.SeqSet{},
		MessageChan: make(chan *imap.Message, 10),
		DoneChan:    make(chan error),
	}

	if _, err := os.Stat(path); err != nil {
		return m, errors.New("File does not exist\n")
	}

	f, err := os.ReadFile(path)
	if err != nil {
		return m, errors.New("File can't be opened\n")
	}

	err = yaml.Unmarshal(f, &m)
	if err != nil {
		return m, errors.New("File can't be read\n")
	}
	log.Printf("Accessing E-Mail-Account %s on %s and moving unread Messages from %s to %s", m.Username, m.IMAPUri, m.SourceFolder, m.DestinationFolder)
	return m, nil
}

func (m *Monitor) Login() error {
	if m.DebugLevel > 0 {
		log.Printf("Logging in as %s on %s. TLS: %t\n", m.Username, m.IMAPUri, m.TLS)
	}
	if m.TLS {
		if m.DebugLevel > 0 {
			log.Printf("Dialing TLS...\n")
		}
		cl, err := client.DialTLS(m.IMAPUri, nil)
		if err != nil {
			return err
		}
		m.Client = cl
	} else {
		if m.DebugLevel > 0 {
			log.Printf("Dialing without TLS...\n")
		}
		cl, err := client.Dial(m.IMAPUri)
		if err != nil {
			return err
		}
		if m.DebugLevel > 0 {
			log.Printf("Starting TLS...\n")
		}
		err = cl.StartTLS(
			nil,
		)
		if err != nil {
			return err
		}
		m.Client = cl
	}
	if m.DebugLevel > 0 {
		log.Printf("Sending Login Request...\n")
	}
	err := m.Client.Login(m.Username, m.Password)
	if err != nil {
		log.Printf("%s - Login failed: %s", m.Username, err)
	}
	if m.DebugLevel > 1 {
		m.Client.SetDebug(os.Stderr)
	}
	log.Println("Logged in!")
	return err
}

func (m *Monitor) CheckConnection() error {
	if m.DebugLevel > 0 {
		log.Printf("Checking ConnectionState: %d\n", m.Client.State())
	}
	if m.Client.State() != imap.ConnectedState &&
		m.Client.State() != imap.AuthenticatedState &&
		m.Client.State() != imap.SelectedState {
		err := m.Login()
		if err != nil {
			log.Printf("%s - Login failed: %s", m.Username, err)
			return err
		}
	}
	return nil
}

func (m *Monitor) Fetch() {
	if m.DebugLevel > 0 {
		log.Printf("Fetching unread Messages from Folder: %s\n", m.SourceFolder)
	}
	if err := m.CheckConnection(); err != nil {
		log.Printf("%s - Connection lost: %s", m.Username, err)
		return
	}

	if m.DebugLevel > 0 {
		log.Printf("Preparing Search Request by Selecting Folder: %s\n", m.SourceFolder)
	}
	criteria := imap.NewSearchCriteria()
	criteria.WithoutFlags = []string{imap.SeenFlag}
	_, err := m.Client.Select(m.SourceFolder, false)
	if err != nil {
		log.Println(err)
	}

	if m.DebugLevel > 0 {
		log.Printf("Sending Search Request...\n")
	}
	ids, err := m.Client.Search(criteria)
	if err != nil {
		log.Println(err)
	}

	if m.DebugLevel > 0 {
		log.Printf("Found %d Messages in %s\n", len(ids), m.SourceFolder)
	}
	if len(ids) <= 0 {
		return
	}

	m.FetchSet.AddNum(ids...)

	if m.DebugLevel > 0 {
		log.Printf("Fetching %d Messages from %s\n", len(ids), m.SourceFolder)
	}
	go func() {
		m.DoneChan <- m.Client.Fetch(m.FetchSet, []imap.FetchItem{"BODY.PEEK[]"}, m.MessageChan) //imap.FetchRFC822}, m.MessageChan)
	}()

	err = m.PrintAll()
	if err != nil {
		log.Println(err)
	}
	log.Printf("Fetching done\n")
}

func (m *Monitor) MarkRead() {
	if m.DebugLevel > 0 {
		log.Printf("Marking Messages read in Folder: %s\n", m.DestinationFolder)
	}

	if err := m.CheckConnection(); err != nil {
		log.Printf("%s - Connection lost: %s", m.Username, err)
		return
	}

	if m.DebugLevel > 0 {
		log.Printf("Preparing Search Request by Selecting Folder: %s\n", m.DestinationFolder)
	}
	criteria := imap.NewSearchCriteria()
	criteria.WithoutFlags = []string{imap.SeenFlag}
	_, err := m.Client.Select(m.DestinationFolder, false)
	if err != nil {
		log.Println("Marking read failed: ", err)
	}

	if m.DebugLevel > 0 {
		log.Printf("Sending Search Request...\n")
	}
	ids, err := m.Client.Search(criteria)
	if err != nil {
		log.Println("Getting unread messages: ", err)
	}

	if m.DebugLevel > 0 {
		log.Printf("Found %d Messages in %s\n", len(ids), m.DestinationFolder)
	}
	if len(ids) <= 0 {
		log.Println("No unread Messages found.")
		return
	}

	m.ReadSet.Clear()
	m.ReadSet.AddNum(ids...)

	if m.DebugLevel > 0 {
		log.Printf("Marking %d Messages read in %s\n", len(ids), m.DestinationFolder)
	}
	item := imap.FormatFlagsOp(imap.AddFlags, true)
	flags := []interface{}{imap.SeenFlag}
	err = m.Client.Store(m.ReadSet, item, flags, nil)
	if err != nil {
		log.Fatal("Marking as read: ", err)
	}
	if m.DebugLevel > 0 {
		log.Printf("Marked %d Messages read in %s\n", len(ids), m.DestinationFolder)
	}
	log.Printf("Marking read done\n")
}

func (m *Monitor) PrintAll() error {
	moveSeqSet := new(imap.SeqSet)
	if m.DebugLevel > 0 {
		log.Printf("Downloading & sending fetched Messages to Default Printer\n")
	}

	for msg := range m.MessageChan {
		var file string
		var err error
		var subject string
		var from string
		var to string
		var cc string
		var date string
		var header []string
		var html int
		var text int

		timestamp := time.Now().Format(layout)

		file = filepath.Join(os.TempDir(), (base64.StdEncoding.EncodeToString([]byte(timestamp))))

		filehtml := file + ".html"
		filetxt := file + ".txt"

		fhtml, err := os.Create(filehtml)
		if err != nil {
			log.Println("File Creation: ", err)
			return err
		}
		defer fhtml.Close()

		ftxt, err := os.Create(filetxt)
		if err != nil {
			log.Println("File Creation: ", err)
			return err
		}
		defer ftxt.Close()

		for _, literal := range msg.Body {
			mr, err := mail.CreateReader(literal)
			if err != nil {
				log.Fatal("Creating Reader:", err)
				return err
			}

			fr, _ := mr.Header.AddressList("From")
			for _, a := range fr {
				from += a.Address + ","
			}
			from = from[:len(from)-1]

			t, _ := mr.Header.AddressList("To")
			for _, a := range t {
				to += a.Address + ","
			}
			to = to[:len(to)-1]

			c, _ := mr.Header.AddressList("Cc")
			if len(c) > 0 {
				for _, a := range c {
					cc += a.Address + ","
				}
				cc = cc[:len(cc)-1]
			}
			date = mr.Header.Get("Date")
			subject = mr.Header.Get("Subject")

			if m.DebugLevel > 0 {
				log.Println(date, from, to, subject, cc)
			}

			if "" != date {
				header = append(
					header,
					("Date:\t\t" + date + "\n"),
				)
			}
			if "" != from {
				header = append(
					header,
					("From:\t\t" + from + "\n"),
				)
			}
			if "" != to {
				header = append(
					header,
					("To:\t\t" + to + "\n"),
				)
			}
			if "" != cc {
				header = append(
					header,
					("Cc:\t\t" + cc + "\n"),
				)
			}
			if "" != subject {
				header = append(
					header,
					("Subject:\t" + subject + "\n"),
				)
			}

			header = append(
				header,
				("====================================\n\n\n\n"),
			)

			for _, s := range header {
				fhtml.WriteString(s)
				if err != nil {
					log.Printf("Writing File (Header): %s\n", err)
				}
				ftxt.WriteString(s)
				if err != nil {
					log.Printf("Writing File (Header): %s\n", err)
				}
			}
			for {
				p, err := mr.NextPart()
				if err == io.EOF {
					break
				} else if err != nil {
					log.Printf("Reading Message: %s\n", err)
					return err
				}

				switch h := p.Header.(type) {
				case *mail.InlineHeader:

					t, _, err := h.ContentType()
					if err != nil {
						log.Printf("Reading ContentType: %s\n", err)
						return err
					}
					switch t {
					case "text/html":
						htmltmp := parseMessage(p.Body, fhtml)
						if htmltmp > 0 {
							html = htmltmp
						}
					case "text/plain":
						texttmp := parseMessage(p.Body, ftxt)
						if texttmp > 0 {
							text = texttmp
						}
					default:
						continue
					}
				}
			}
			moveSeqSet.AddNum(msg.Uid)
			err = m.Client.Move(moveSeqSet, m.DestinationFolder)
			if err != nil {
				log.Println("Moving Item: ", err)
			}
		}
		if text > 0 {
			file = filetxt
		} else if html > 0 {
			file = filehtml
		}

		err = printLinux(file, true)
		if err != nil {
			log.Println("Printing: ", err)
			return err
		}
	}

	m.MessageChan = make(chan *imap.Message)
	return nil
}

func parseMessage(r io.Reader, f *os.File) int {
	var err error
	content, _ := io.ReadAll(r)
	i, err := f.Write(content)
	if err != nil {
		log.Printf("Writing File (%s): %s", f.Name(), err)
	}
	return i
}

func CheckPrerequisites() error {
	var err error
	_, err = exec.LookPath("wkhtmltopdf")
	if err != nil {
		log.Println(err)
		return err
	}
	_, err = exec.LookPath("lp")
	if err != nil {
		log.Println(err)
		return err
	}
	return err
}

func printLinux(path string, delete bool) error {
	lp, err := exec.LookPath("lp")
	if err != nil {
		log.Println(err)
		return err
	}
	if strings.Contains(path, "html") {
		npath := strings.ReplaceAll(path, ".html", ".pdf")

		wkhtmltopdf, err := exec.LookPath("wkhtmltopdf")
		if err != nil {
			log.Println(err)
			return err
		}

		cmd := exec.Command(wkhtmltopdf, path, npath)
		_ = cmd.Run()
		if delete {
			err = os.Remove(path)
			if err != nil {
				log.Println(err)
			}
		}
		path = npath
	}
	cmd := exec.Command(lp, path)
	_ = cmd.Run()

	if delete {
		err = os.Remove(path)
		if err != nil {
			log.Println(err)
		}
	}

	return err
}

func Run() {
	var m Monitor
	var err error

	argLength := len(os.Args[1:])
	if err := CheckPrerequisites(); err != nil {
		log.Fatal(err)
	}
	switch {
	case argLength <= 0:
		if _, err := os.Stat("settings.yaml"); err != nil {
			log.Fatal("Required Arguments missing!")
		}
		m, err = NewMonitorFromFile("settings.yaml")
		if err != nil {
			log.Fatal(err)
		}

	case argLength >= 1:
		m, err = NewMonitorFromFile(os.Args[1])
		if err != nil {
			log.Fatal(err)
		}

		if argLength > 1 {
			d, err := strconv.ParseInt(os.Args[2], 10, 64)
			if err != nil {
				log.Fatal(err)
			}
			m.DebugLevel = d
		}

	default:
		log.Fatal("Invalid or missing Parameters. Make sure to pass the pass to the 'settings.yaml'")
	}
	err = m.Login()
	if err != nil {
		log.Fatal(err)
	}
	m.Fetch()
	m.MarkRead()
}
