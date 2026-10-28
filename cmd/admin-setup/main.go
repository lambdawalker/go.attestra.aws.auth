package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider/types"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/lambdawalker/go.attestra.aws.auth/adminsetup"
	"github.com/lambdawalker/go.attestra.aws.auth/runtimeconfig"
	"log"
	"net/mail"
	"os"
	"strings"
)

func main() {
	authOnly := flag.Bool("auth-only", false, "Reconcile admin authentication only; do not invite users or change grants")
	pool := flag.String("pool", "", "admin pool")
	login := flag.String("login", "", "managed login origin")
	table := flag.String("table", "", "runtime table")
	email := flag.String("email", "", "first administrator email")
	environment := flag.String("environment", "", "environment grant")
	flag.Parse()
	if e := validateSetupArgs(*authOnly, *pool, *login, *table, *email, *environment); e != nil {
		log.Fatal(e)
	}
	ctx := context.Background()
	cfg, e := config.LoadDefaultConfig(ctx)
	if e != nil {
		log.Fatal(e)
	}
	c := cognitoidentityprovider.NewFromConfig(cfg)
	if e = adminsetup.Configure(ctx, c, *pool, *login); e != nil {
		log.Fatal(e)
	}
	if *authOnly {
		fmt.Println("Admin authentication reconciled; users and grants unchanged.")
		return
	}
	store := runtimeconfig.Store{DB: dynamodb.NewFromConfig(cfg), Table: *table}
	done, e := store.Bootstrapped(ctx, *environment)
	if e != nil {
		log.Fatal(e)
	}
	if done {
		fmt.Println("Admin MFA verified; bootstrap grant already recorded and preserved.")
		return
	}
	user, e := c.AdminGetUser(ctx, &cognitoidentityprovider.AdminGetUserInput{UserPoolId: pool, Username: email})
	if e != nil {
		var missing *types.UserNotFoundException
		if !errors.As(e, &missing) {
			log.Fatal(e)
		}
		temporary := make([]byte, 24)
		if _, e = rand.Read(temporary); e != nil {
			log.Fatal("temporary password generation failed")
		}
		password := base64.RawURLEncoding.EncodeToString(temporary) + "Aa1!"
		created, e := c.AdminCreateUser(ctx, &cognitoidentityprovider.AdminCreateUserInput{UserPoolId: pool, Username: email, TemporaryPassword: aws.String(password), DesiredDeliveryMediums: []types.DeliveryMediumType{types.DeliveryMediumTypeEmail}, UserAttributes: []types.AttributeType{{Name: aws.String("email"), Value: email}, {Name: aws.String("email_verified"), Value: aws.String("true")}}})
		if e != nil {
			log.Fatal(e)
		}
		user = &cognitoidentityprovider.AdminGetUserOutput{UserAttributes: created.User.Attributes}
	}
	sub := ""
	for _, a := range user.UserAttributes {
		if aws.ToString(a.Name) == "sub" {
			sub = aws.ToString(a.Value)
		}
	}
	if sub == "" {
		fmt.Fprintln(os.Stderr, "admin subject unavailable; no grant written")
		os.Exit(1)
	}
	if e = store.BootstrapGrant(ctx, sub, *environment); e != nil {
		if errors.Is(e, runtimeconfig.ErrConflict) {
			done, readErr := store.Bootstrapped(ctx, *environment)
			if readErr == nil && done {
				fmt.Println("Existing bootstrap grant preserved.")
				return
			}
		}
		log.Fatal("bootstrap grant could not be recorded; existing grants were preserved: ", e)
	}
	fmt.Println("Admin MFA verified; first environment grant recorded.")
}

func validateSetupArgs(authOnly bool, pool, login, table, email, environment string) error {
	if pool == "" || login == "" {
		return errors.New("pool and login required")
	}
	if authOnly {
		if table != "" || email != "" || environment != "" {
			return errors.New("auth-only cannot be combined with invitation or environment grant arguments")
		}
		return nil
	}
	if !runtimeconfig.ValidEnvironment(environment) || table == "" {
		return errors.New("table and environment required")
	}
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email || strings.ToLower(email) != email {
		return errors.New("valid lowercase bootstrap email required")
	}
	return nil
}
