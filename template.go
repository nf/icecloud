package main

import (
	"fmt"
	"io"
	"text/template"
)

func SetupTemplate(w io.Writer, icecast *Icecast, server, master *Server) error {
	err := setupTmpl.Execute(w, struct {
		Icecast        *Icecast
		Server, Master *Server
	}{icecast, server, master})
	if err != nil {
		return fmt.Errorf("SetupTemplate: %v", err)
	}
	return nil
}

var setupTmpl *template.Template

func init() {
	setupTmpl = template.New("setup")
	template.Must(setupTmpl.Parse(setupText))
}

const setupText = `#!/bin/bash

sudo apt-get -qq -y install icecast2

cat > etc_icecast2_icecast.xml <<EOF
<icecast>
    <limits>
        <clients>{{.Server.NumClients}}</clients>
        <sources>{{.Server.NumSources}}</sources>
        <threadpool>5</threadpool>
        <queue-size>524288</queue-size>
        <client-timeout>30</client-timeout>
        <header-timeout>15</header-timeout>
        <source-timeout>10</source-timeout>
        <burst-on-connect>1</burst-on-connect>
        <burst-size>65535</burst-size>
    </limits>

    <authentication>
        <!-- Sources log in with username 'source' -->
        <source-password>{{.Icecast.SourcePassword}}</source-password>
        <!-- Relays log in username 'relay' -->
        <relay-password>{{.Icecast.RelayPassword}}</relay-password>
        <!-- Admin logs in with the username given below -->
        <admin-user>admin</admin-user>
        <admin-password>{{.Icecast.AdminPassword}}</admin-password>
    </authentication>

    <hostname>{{.Server.Instance.DNSName}}</hostname>

    <listen-socket>
        <port>{{.Icecast.ListenPort}}</port>
    </listen-socket>

{{if .Master}}
    <master-server>{{.Master.Instance.DNSName}}</master-server>
    <master-server-port>{{.Icecast.ListenPort}}</master-server-port>
    <master-update-interval>5</master-update-interval>
    <master-password>{{.Icecast.RelayPassword}}</master-password>
{{end}}

    <fileserve>1</fileserve>

    <paths>
        <logdir>/var/log/icecast2</logdir>
        <webroot>/usr/share/icecast2/web</webroot>
        <adminroot>/usr/share/icecast2/admin</adminroot>
        <alias source="/" dest="/status.xsl"/>
    </paths>

    <logging>
        <accesslog>access.log</accesslog>
        <errorlog>error.log</errorlog>
      	<loglevel>3</loglevel> <!-- 4 Debug, 3 Info, 2 Warn, 1 Error -->
      	<logsize>10000</logsize> <!-- Max size of a logfile -->
        <logarchive>1</logarchive>
    </logging>

    <security>
        <chroot>0</chroot>
    </security>
</icecast>
EOF

cat > etc_default_icecast2 <<EOF
# Defaults for icecast2 initscript
# sourced by /etc/init.d/icecast2
# installed at /etc/default/icecast2 by the maintainer scripts

#
# This is a POSIX shell fragment
#

# Full path to the server configuration file
CONFIGFILE="/etc/icecast2/icecast.xml"

# Name or ID of the user and group the daemon should run under
USERID=icecast2
GROUPID=icecast

# Edit /etc/icecast2/icecast.xml and change at least the passwords.
# Change this to true when done to enable the init.d script
ENABLE=true

EOF

sudo cp etc_default_icecast2 /etc/default/icecast2
sudo cp etc_icecast2_icecast.xml /etc/icecast2/icecast.xml
sudo chown icecast2:icecast /etc/icecast2/icecast.xml
sudo chmod 660 /etc/icecast2/icecast.xml

sudo /etc/init.d/icecast2 restart
`

var m3uTmpl = template.Must(template.New("m3u").Parse(
	"{{range .}}{{.}}\n{{end}}"))

var plsTmpl = template.Must(template.New("m3u").Funcs(template.FuncMap{
	"idx": func(i int) int { return i + 1 },
}).Parse(`
[playlist]
{{range $i, $s := .}}
File{{idx $i}}={{$s}}
Title{{idx $i}}=Server {{idx $i}}
Length{{idx $i}}=-1
{{end}}
NumberOfEntries={{len .}}
Version=2
`))
