// Package options defines GlobalOptions, the flags every kurel command shares
// (configuration file, verbose, debug and strict modes, output format and file,
// namespace, dry run), with AddFlags to register them, Complete to read some of
// them from Viper configuration, and Validate to check them.
package options
