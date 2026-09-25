package awsemail

import (
	"context"
	"log"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sesv2/types"
)

type Sender struct {
	Client *sesv2.Client
	From   string
}

func (s Sender) Send(ctx context.Context, to, link, code string) error {
	message := "Verify your email by opening this link:\n" + link + "\n\nIf you opened it on another device, enter this six-digit code on that page: " + code + "\n\nThis email expires in 10 minutes. Use only the newest email after requesting another. If you did not request it, ignore this message."
	out, err := s.Client.SendEmail(ctx, &sesv2.SendEmailInput{FromEmailAddress: aws.String(s.From), Destination: &types.Destination{ToAddresses: []string{to}}, Content: &types.EmailContent{Simple: &types.Message{Subject: &types.Content{Data: aws.String("Verify your Attestra email"), Charset: aws.String("UTF-8")}, Body: &types.Body{Text: &types.Content{Data: aws.String(message), Charset: aws.String("UTF-8")}}}}})
	if err == nil {
		log.Printf("ses_message_accepted message_id=%s", aws.ToString(out.MessageId))
	}
	return err
}
