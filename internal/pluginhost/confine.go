package pluginhost

// ConfineCommand is the argument rta is run with to become a plugin under
// strict confinement on Linux (ConfineMain): main hands it over before doing
// anything else, and wrap is what puts it on a command line.
const ConfineCommand = "__confine"
