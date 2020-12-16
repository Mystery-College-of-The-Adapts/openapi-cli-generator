package cli

import (
	"fmt"
	"golang.org/x/oauth2"
	"gopkg.in/h2non/gentleman.v2/context"
	"net/http"
	"os"
	"strings"

	"github.com/olekukonko/tablewriter"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// AuthHandler describes a handler that can be called on a request to inject
// auth information and is agnostic to the type of auth.
type AuthHandler interface {
	ExecuteFlow(log *zerolog.Logger) (*oauth2.Token, error)

	// ProfileKeys returns the key names for fields to store in the profileName.
	ProfileKeys() []string

	// OnRequest gets run before the request goes out on the wire.
	OnRequest(log *zerolog.Logger, request *http.Request) error
}

// AuthHandlers is the map of registered auth type names to handlers
var AuthHandlers = make(map[string]AuthHandler)

var authInitialized bool
var authCommand *cobra.Command

// AddAuthCommands sets up basic commands and the credentials file so that new auth
// handlers can be registered. This MUST be called only after auth handlers have
// been set up through UseAuth.
func AddAuthCommands(parent *cobra.Command) {
	// Add base auth commands
	authCommand = &cobra.Command{
		Use:   "auth",
		Short: "Authentication settings",
	}

	authCommand.AddCommand(initAuthAddServersCommand())
	authCommand.AddCommand(initAuthAddCredentialsCommand())
	authCommand.AddCommand(initAuthListServersCommand())
	authCommand.AddCommand(initAuthListCredentialsCommand())

	parent.AddCommand(authCommand)
}

func initAuthAddServersCommand() *cobra.Command {
	var clientID string
	var issuer string
	cmd := &cobra.Command{
		Use:   "add-server",
		Short: "Add a new authentication server",
		Args:  cobra.ExactArgs(1),
		Run:  func(cmd *cobra.Command, args []string) {
			logger := log.With().Str("profile", RunConfig.ProfileName).Logger()

			authServerName := strings.Replace(args[0], ".", "-", -1)
			_, exists := RunConfig.Settings.AuthServers[authServerName]
			if exists {
				logger.Fatal().Msgf("credential %q already exists", authServerName)
			}

			updates := make(map[string]interface{})
			updates[fmt.Sprintf("auth_servers.%s.issuer", authServerName)] = issuer
			updates[fmt.Sprintf("auth_servers.%s.client_id", authServerName)] = clientID
			err := RunConfig.write(RunConfig.settingsPath, updates)
			if err != nil {
				logger.Fatal().Err(err)
			}
		},
	}
	cmd.Flags().StringVar(&clientID, "client-id", "", "")
	cmd.Flags().StringVar(&issuer, "issuer", "", "")

	/*
	SetCustomFlags(cmd)

	if cmd.Flags().HasFlags() {
		params.BindPFlags(cmd.Flags())
	}
	 */
	return cmd
}

func initAuthAddCredentialsCommand() *cobra.Command {
	var authServerName string

	cmd := &cobra.Command{
		Use:   "add-credentials",
		Short: "Add a new set of credentials",
		Args:  cobra.ExactArgs(1),
		Run:  func(cmd *cobra.Command, args []string) {
			logger := log.With().Str("profile", RunConfig.ProfileName).Logger()

			credentialName := strings.Replace(args[0], ".", "-", -1)
			_, exists := RunConfig.Secrets.Credentials[credentialName]
			if exists {
				logger.Fatal().Msgf("credential %q already exists", credentialName)
			}

			handler := AuthHandlers[authServerName]
			token, err := handler.ExecuteFlow(&logger)
			if err != nil {
				logger.Fatal().Err(err)
			}
			err = RunConfig.UpdateCredentialsToken(credentialName, token)
			if err != nil {
				logger.Fatal().Err(err)
			}
		},
	}
	cmd.Flags().StringVar(&authServerName, "auth-server-name", "", "")

	/*
	SetCustomFlags(cmd)

	if cmd.Flags().HasFlags() {
		params.BindPFlags(cmd.Flags())
	}
	 */
	return cmd
}

func initAuthListCredentialsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "list-credentials",
		Short:   "List available credentials",
		Args:    cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			credentials := RunConfig.Secrets.Credentials
			if credentials != nil {
				table := tablewriter.NewWriter(os.Stdout)
				table.SetHeader([]string{"Name", "Client ID", "Issuer"})

				for credentialName, credential := range credentials {
					table.Append([]string{credentialName, credential.TokenPayload.ClientID(), credential.TokenPayload.Issuer()})
				}
				table.Render()
			} else {
				fmt.Printf("No credentials configured. Use `%s auth addCredentials` to add one.\n", Root.CommandPath())
			}
		},
	}
	return cmd
}

func initAuthListServersCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "list-servers",
		Short:   "List available authentication servers",
		Args:    cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			authServers := RunConfig.Settings.AuthServers
			if authServers != nil {
				table := tablewriter.NewWriter(os.Stdout)
				table.SetHeader([]string{"Name", "Client ID", "Issuer"})

				// For each type name, draw a table with the relevant profileName keys
				for authServerName, authServer := range authServers {
					table.Append([]string{authServerName, authServer.ClientID, authServer.Issuer})
				}
				table.Render()
			} else {
				fmt.Printf("No authentication servers configured. Use `%s auth addServer` to add one.\n", Root.CommandPath())
			}
		},
	}
	return cmd
}

// UseAuth registers a new auth handler for a given type name. For backward-
// compatibility, the auth type name can be a blank string. It is recommended
// to always pass a value for the type name.
func UseAuth(typeName string, handler AuthHandler) {
	if !authInitialized {
		// Install auth middleware
		Client.UseRequest(func(ctx *context.Context, h context.Handler) {
			handler := AuthHandlers[RunConfig.GetProfile().AuthServerName]
			if handler == nil {
				h.Error(ctx, fmt.Errorf("no handler for auth server %q", RunConfig.GetProfile().AuthServerName))
				return
			}

			if err := handler.OnRequest(ctx.Get("log").(*zerolog.Logger), ctx.Request); err != nil {
				h.Error(ctx, err)
				return
			}

			h.Next(ctx)
		})
		authInitialized = true
	}

	// Register the handler by its type.
	AuthHandlers[typeName] = handler
}
