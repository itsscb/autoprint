# autoprint
Downloads E-Mails from a specific folder and sends them to the Default Printer via CUPS (lp)

## Requirements
- *OS*: Any Linux should do
- *Software*:
	- CUPS
	- wkhtmltopdf
- *Hardware*:
	- Any Linux compatible Printer should do

## Setup
- Clone this Repository: ```git clone https://github.com/itsscb/autoprint.git```
- Build the binary: ```go build```
- Create the *settings.yaml*-file
	- Example ```IMAPUri:  imap.provider.com:993
Username: user@example.com
Password: P@$$w0Rd
TLS: true # TLS = true, STARTTLS = false
SourceFolder: INBOX/Print # Where to get the E-Mails
DestinationFolder:  INBOX/Printed # Where to move the E-Mails afterwards
Debug: 0 # 1 = Verbose, 2 = Debug```
- Run the binary
	- Optinal using *cron*
