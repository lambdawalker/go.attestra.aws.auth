package main

import "fmt"

// captureWorkerDatabaseStatements supplies the table operations used by worker swaps.
// Only account completion requires UpdateItem; API and other records keep their existing grants.
func captureWorkerDatabaseStatements(tableARN string) string {
	return fmt.Sprintf(`{"Effect":"Allow","Action":["dynamodb:GetItem","dynamodb:PutItem"],"Resource":%q},{"Effect":"Allow","Action":["dynamodb:UpdateItem"],"Resource":%q,"Condition":{"ForAllValues:StringLike":{"dynamodb:LeadingKeys":["ACCOUNT#*"]}}}`, tableARN, tableARN)
}
