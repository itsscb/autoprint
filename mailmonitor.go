package main

import (
	"errors"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	imap "github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
	"github.com/emersion/go-message/mail"
	"github.com/google/uuid"
	"gopkg.in/yaml.v2"
)

type Monitor struct {
	IMAPUri           string `yaml:"IMAPUri"`
	Username          string `yaml:"Username"`
	Password          string `yaml:"Password"`
	SourceFolder      string `yaml:"SourceFolder"`
	DestinationFolder string `yaml:"DestinationFolder"`
	TLS               bool   `yaml:"TLS"`
	DebugLevel        int64  `yaml:"DebugLevel"`
	Files             []string
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

func printLinux(path string) error {
	var err error
	var npath string

	lp, err := exec.LookPath("lp")
	if err != nil {
		log.Println(err)
		return err
	}
	if strings.Contains(path, "html") {
		npath = strings.ReplaceAll(path, ".html", ".pdf")

		wkhtmltopdf, err := exec.LookPath("wkhtmltopdf")
		if err != nil {
			log.Println(err)
			return err
		}

		convertCMD := exec.Command(wkhtmltopdf, path, npath)
		_ = convertCMD.Run()

		path = npath
	}
	printCMD := exec.Command(lp, path)
	_ = printCMD.Run()

	if npath != "" {
		err = os.Remove(npath)
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

	var section imap.BodySectionName
	items := []imap.FetchItem{section.FetchItem()}

	go func() {
		m.DoneChan <- m.Client.Fetch(m.FetchSet, items, m.MessageChan) //imap.FetchRFC822}, m.MessageChan)
	}()

	err = m.PrintAll()
	if err != nil {
		log.Println(err)
	}
	log.Printf("Fetching done\n")
}

func (m *Monitor) PrintAll() error {
	var err error

	if m.DebugLevel > 0 {
		log.Printf("Sending fetched Messages to Default Printer\n")
	}

	for msg := range m.MessageChan {

		for _, literal := range msg.Body {
			mr, err := mail.CreateReader(literal)
			if err != nil {
				log.Fatalf("Could not create Reader: %s", err)
			}

			uid := uuid.New()
			fp := filepath.Join(os.TempDir(), uid.String())
			if m.DebugLevel > 0 {
				log.Printf("Creating temporary File: %s\n", fp)
			}

			// var wb int64
			var tfp string
			for {
				p, err := mr.NextPart()
				if err == io.EOF {
					break
				} else if err != nil {
					log.Fatal(err)
				}

				ct := p.Header.Get("Content-Type")

				switch h := p.Header.(type) {
				case *mail.InlineHeader:
					if strings.Contains(ct, "text/html") {
						tfp = fp + ".html"
					} else if strings.Contains(ct, "text/plain") {
						tfp = fp + ".txt"
					} else if strings.Contains(ct, "application/pdf") || strings.Contains(ct, "image") {
						filename := strings.Split(
							strings.Split(ct, ";")[1],
							"=")[1]
						tfp = fp + filename
					}

				case *mail.AttachmentHeader:
					var filename string
					if strings.Contains(ct, "text/html") {
						tfp = fp + ".html"
					} else if strings.Contains(ct, "text/plain") {
						tfp = fp + ".txt"
					} else if strings.Contains(ct, "application/pdf") || strings.Contains(ct, "image") {
						filename, _ = h.Filename()
						tfp = fp + filename
					} else {
						continue
					}
					tfp = fp + filename
				}

				f, err := os.Create(tfp)
				if err != nil {
					log.Fatalf("Couldn't create temporary File %s: %s\n", tfp, err)
				}
				defer f.Close()

				var exists bool
				for _, file := range m.Files {
					if file == tfp {
						exists = true
					}
				}

				if !exists {
					m.Files = append(m.Files, tfp)
				}

				if m.DebugLevel > 0 {
					log.Printf("Got %v: %s\n", p.Header.Get("Content-Type"), tfp)
				}

				r, err := io.ReadAll(p.Body)
				if err != nil {
					log.Fatalf("Could not read to Content: %s\n", err)
				}
				_, err = f.Write(r)
				if err != nil {
					log.Fatalf("Could not write to File %s: %s", tfp, err)
				}
			}
		}
	}

	for _, f := range m.Files {
		fi, err := os.Stat(f)
		if err != nil {
			log.Printf("Failed to read File %s: %s\n", f, err)
		}

		if fi.Size() > 0 {
			var htmlexists bool
			if strings.HasSuffix(f, ".txt") {
				for _, tf := range m.Files {
					if tf == strings.Replace(f, ".txt", ".html", -1) {
						if m.DebugLevel > 0 {
							log.Printf("Found match for %s: %s - Skipping .txt-File\n", f, tf)
						}
						htmlexists = true
					}
				}
			}

			if htmlexists {
				continue
			}
			err := printLinux(f)
			if err != nil {
				log.Printf("Failed to print File %s: %s\n", f, err)
			}
		}
		err = os.Remove(f)
		if err != nil {
			log.Printf("Failed to delete File %s: %s\n", f, err)
		}
	}

	return err
}
