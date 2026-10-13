package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/lambdawalker/go.attestra.aws.auth/assetlinks"
	"github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/ui"
)

func removeAssociation(apps []assetlinks.Application, pkg, fingerprint string) []assetlinks.Application {
	result := make([]assetlinks.Application, 0, len(apps))
	for _, app := range apps {
		kept := []string{}
		for _, fp := range app.Fingerprints {
			if app.PackageName != pkg || !strings.EqualFold(fp, fingerprint) {
				kept = append(kept, fp)
			}
		}
		if len(kept) > 0 {
			result = append(result, assetlinks.Application{PackageName: app.PackageName, Fingerprints: kept})
		}
	}
	return result
}

func publishAssociations(r commandRunner, o options, apps []assetlinks.Application) error {
	if _, err := assetlinks.Render(apps); err != nil {
		return err
	}
	if apps == nil {
		apps = []assetlinks.Application{}
	}
	data, _ := json.Marshal(apps)
	infra := filepath.Join(o.Root, "infra")
	if _, err := r.Exec(infra, false, "pulumi", "config", "set", "--stack", o.Stack, "--non-interactive", "attestra-auth-email:androidAssociations", "--", string(data)); err != nil {
		return err
	}
	target := "urn:pulumi:" + o.Stack + "::attestra-auth-email::aws:s3/bucketObjectv2:BucketObjectv2::android-assetlinks"
	if _, err := r.Exec(infra, false, "pulumi", "up", "--stack", o.Stack, "--non-interactive", "--yes", "--skip-preview", "--target", target); err != nil {
		return fmt.Errorf("association publish failed; edited Pulumi configuration retained for retry: %w", err)
	}
	return waitAssetLinks(r, o)
}

func manageAssociations(r commandRunner, o options) error {
	if err := checkStack(r, o); err != nil {
		return err
	}
	infra := filepath.Join(o.Root, "infra")
	raw, err := r.Exec(infra, true, "pulumi", "stack", "output", "--json", "--stack", o.Stack)
	if err != nil {
		return err
	}
	var outputs struct {
		AssetLinksURL string `json:"assetLinksUrl"`
	}
	if json.Unmarshal(raw, &outputs) != nil || outputs.AssetLinksURL == "" {
		return errors.New("association hosting is not deployed; run setup first")
	}
	raw, err = r.Exec(infra, true, "pulumi", "config", "--json", "--stack", o.Stack)
	if err != nil {
		return err
	}
	var config map[string]struct{ Value string }
	if json.Unmarshal(raw, &config) != nil {
		return errors.New("invalid stack configuration")
	}
	var apps []assetlinks.Application
	if json.Unmarshal([]byte(config["attestra-auth-email:androidAssociations"].Value), &apps) != nil {
		return errors.New("invalid androidAssociations configuration")
	}
	if _, err := assetlinks.Render(apps); err != nil {
		return err
	}
	fmt.Println("Environment:", o.Stack, "\nPublic file:", outputs.AssetLinksURL)
	fmt.Println("Changes stay in memory until Publish. Publishing saves Pulumi YAML and updates only the association object, skipping preview. Commit the YAML afterwards. Avoid concurrent config edits.")
	dirty := false
	for {
		action := "list"
		if err := huh.NewSelect[string]().Title("Android signing associations").Options(huh.NewOption("List approvals", "list"), huh.NewOption("Add from public JSON", "add"), huh.NewOption("Remove a fingerprint", "remove"), huh.NewOption("Publish current list (no full deployment)", "publish"), huh.NewOption("Exit", "exit")).Value(&action).Run(); err != nil {
			return err
		}
		switch action {
		case "list":
			if len(apps) == 0 {
				fmt.Println("No approved signing identities.")
			}
			for _, app := range apps {
				fmt.Println(app.PackageName)
				for _, fp := range app.Fingerprints {
					fmt.Println("  " + fp)
				}
			}
		case "add":
			home, _ := os.UserHomeDir()
			path := filepath.Join(home, ".attestra", "associations", o.Stack, "developer-associations.json")
			if err := ui.Input("Public association JSON file", &path, false, true).Run(); err != nil {
				return err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				fmt.Println("Cannot read file:", err)
				continue
			}
			var incoming []assetlinks.Application
			if json.Unmarshal(data, &incoming) != nil {
				fmt.Println("Invalid association JSON")
				continue
			}
			merged, changed, err := mergeAssociations(apps, incoming)
			if err != nil {
				fmt.Println("Cannot add:", err)
				continue
			}
			apps = merged
			dirty = dirty || changed
		case "remove":
			choices := []huh.Option[int]{}
			type identity struct{ pkg, fp string }
			ids := []identity{}
			for _, app := range apps {
				for _, fp := range app.Fingerprints {
					ids = append(ids, identity{app.PackageName, fp})
					choices = append(choices, huh.NewOption(app.PackageName+" • "+fp, len(ids)-1))
				}
			}
			if len(ids) == 0 {
				fmt.Println("No fingerprints to remove.")
				continue
			}
			selected := 0
			if err := huh.NewSelect[int]().Title("Fingerprint to remove").Options(choices...).Value(&selected).Run(); err != nil {
				return err
			}
			apps = removeAssociation(apps, ids[selected].pkg, ids[selected].fp)
			dirty = true
			fmt.Println("Removal staged. A future setup may offer the fingerprint again if it remains in your local handoff JSON; do not reapprove it.")
		case "publish":
			content, _ := assetlinks.Render(apps)
			fmt.Println("Publish to", outputs.AssetLinksURL, "\n"+content)
			approve := false
			if err := huh.NewConfirm().Title("Publish this exact approval list to " + o.Stack + "?").Value(&approve).Run(); err != nil {
				return err
			}
			if !approve {
				continue
			}
			if err := publishAssociations(r, o, apps); err != nil {
				fmt.Println(err)
				continue
			}
			dirty = false
			fmt.Println("Published and verified. Commit infra/Pulumi." + o.Stack + ".yaml. Device/provider caches may delay revocation; sessions are not revoked.")
		case "exit":
			if dirty {
				discard := false
				if err := huh.NewConfirm().Title("Discard unpublished in-memory changes?").Value(&discard).Run(); err != nil {
					return err
				}
				if !discard {
					continue
				}
			}
			return nil
		}
	}
}
