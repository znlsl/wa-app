package app

import (
	"strings"

	waappv1 "github.com/byte-v-forge/wa-app/gen/go/byte/v/forge/waapp/v1"
)

type waKnownContactAlias struct {
	Name        string
	JIDs        []string
	Numbers     []string
	BusinessIDs []string
}

var waKnownContactAliases = []waKnownContactAlias{
	{
		Name:        "OpenAI",
		JIDs:        []string{"227775403311132@lid"},
		Numbers:     []string{"18668392077"},
		BusinessIDs: []string{"1191555928498480"},
	},
}

func normalizedWAContactForStorage(contact *waappv1.WAContact) *waappv1.WAContact {
	if contact == nil {
		return nil
	}
	clone := *contact
	normalizeWAContactNames(&clone)
	return &clone
}

func normalizeWAContactNames(contact *waappv1.WAContact) {
	if contact == nil {
		return
	}
	alias := knownWAContactAliasName(contact)
	contact.DisplayName = resolvedWAContactName(contact.GetDisplayName(), contact.GetNumber())
	contact.WaName = resolvedWAContactName(contact.GetWaName(), contact.GetNumber())
	contact.VerifiedName = resolvedWAContactName(contact.GetVerifiedName(), contact.GetNumber())
	if alias == "" {
		return
	}
	contact.DisplayName = alias
	if contact.GetWaName() == "" {
		contact.WaName = alias
	}
	contact.Kind = waappv1.WAContactKind_WA_CONTACT_KIND_BUSINESS
}

func knownWAContactAliasName(contact *waappv1.WAContact) string {
	if contact == nil {
		return ""
	}
	jid := normalizeWAJID(contact.GetJid())
	number := digitsOnly(contact.GetNumber())
	businessIDs := uniqueStrings(digitsOnly(contact.GetDisplayName()), digitsOnly(contact.GetWaName()), digitsOnly(contact.GetVerifiedName()))
	for _, alias := range waKnownContactAliases {
		if stringInSlice(jid, alias.JIDs) || stringInSlice(number, alias.Numbers) {
			return alias.Name
		}
		for _, businessID := range businessIDs {
			if stringInSlice(businessID, alias.BusinessIDs) {
				return alias.Name
			}
		}
	}
	return ""
}

func resolvedWAContactName(value string, number string) string {
	name := waContactName(value)
	if contactNameNeedsResolution(name, number) {
		return ""
	}
	return name
}

func contactNameNeedsResolution(name string, number string) bool {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return true
	case name == "0" || name == "未知联系人":
		return true
	case strings.HasPrefix(name, "联系人 ") || strings.HasPrefix(name, "LID ") || strings.HasPrefix(name, "企业账号 "):
		return true
	case isNumericWAContactName(name):
		return true
	}
	number = digitsOnly(number)
	return number != "" && (name == "+"+number || digitsOnly(name) == number)
}

func isNumericWAContactName(value string) bool {
	value = strings.TrimPrefix(strings.TrimSpace(value), "+")
	if len(value) < 6 {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func stringInSlice(value string, values []string) bool {
	if value == "" {
		return false
	}
	for _, item := range values {
		if value == item {
			return true
		}
	}
	return false
}
