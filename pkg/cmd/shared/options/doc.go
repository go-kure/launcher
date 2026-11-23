// Package options defines GlobalOptions, the flags every kurel command shares
// (configuration file, verbose, debug and strict modes, output format and file,
// namespace, dry run), with AddFlags to register them, Complete to bind them to
// Viper, and Validate to check them.
package options
