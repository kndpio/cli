package environment

type Cmd struct {
	Create createCmd `cmd:"" help:"Create an Environment"`
	Delete deleteCmd `cmd:"" help:"Delete an Environment"`
	Copy   copyCmd   `cmd:"" help:"Copy an Environment to another destination context"`
	List   listCmd   `cmd:"" help:"List of Environments"`
}
