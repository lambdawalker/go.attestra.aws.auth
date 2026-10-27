package main

import (
	"context"
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
	pool := flag.String("pool", "", "admin pool")
	login := flag.String("login", "", "managed login origin")
	table := flag.String("table", "", "runtime table")
	email := flag.String("email", "", "first administrator email")
	environment := flag.String("environment", "", "environment grant")
	flag.Parse()
	if !runtimeconfig.ValidEnvironment(*environment) || *pool == "" || *table == "" {
		log.Fatal("pool, table and environment required")
	}
	address, e := mail.ParseAddress(*email)
	if e != nil || address.Address != *email || strings.ToLower(*email) != *email {
		log.Fatal("valid lowercase bootstrap email required")
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
		created, e := c.AdminCreateUser(ctx, &cognitoidentityprovider.AdminCreateUserInput{UserPoolId: pool, Username: email, DesiredDeliveryMediums: []types.DeliveryMediumType{types.DeliveryMediumTypeEmail}, UserAttributes: []types.AttributeType{{Name: aws.String("email"), Value: email}, {Name: aws.String("email_verified"), Value: aws.String("true")}}})
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
	if e = store.BootstrapGrant(ctx, sub, *environment); e != nil && !errors.Is(e, runtimeconfig.ErrConflict) {
		log.Fatal(e)
	}
	fmt.Println("Admin MFA verified; first environment grant recorded.")
}
