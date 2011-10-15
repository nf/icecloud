Icecloud is a tool for setting up Icecast streaming networks on Amazon EC2

It was written by [Andrew Gerrand](mailto:adg@golang.org).

== Setup ==

First, [install Go](http://golang.org/doc/install.html).

Install icecloud:

	goinstall github.com/nf/icecloud

== Usage ==

Configure icecloud by copying `config.json` from the source directory to
a working directory somewhere.

Run the VM instances:

	icecloud run config.json

Set up the icecast2 services on the VMs:

	icecloud setup

Generate m3u playlist files for various mount points:

	icecloud playlist mount1 mount2

Shut down the VM instances:

	icecloud shutdown

