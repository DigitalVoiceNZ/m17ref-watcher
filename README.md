# m17ref-watcher

Watch m17 reflector activity and send transmittion notifications
to a MQTT broker.  This version of the watcher monitors changes
in the `mrefd.xml` file for stations whose lastheard time is
newer than the last reported lastheard.

## Installation

Copy `env.sample` to `.env` and edit that file to specify the
MQTT broker address and port.

## Usage

Invoke as `./m17ref-watcher`.  While prototyping, this can be
done within a terminal multiplexer like **tmux** or **screen**.
