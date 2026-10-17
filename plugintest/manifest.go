package plugintest

import (
	"context"
	"encoding/json"
	"net"
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"google.golang.org/grpc"

	pluginv1 "github.com/kryspal/plugin-sdk/pluginv1"
)

// Limits from PROTOCOL.md §5.2, §7 and §7.8.
const (
	maxTextBytes          = 64 << 10
	maxImageFetchRules    = 32
	maxRuleHosts          = 16
	maxAttributeLabels    = 64
	maxAttributeLabelLen  = 64
	maxAuthorLen          = 64
	maxHomepageLen        = 512
	minTaskIntervalMinute = 5
	maxTaskIntervalMinute = 44640
	taskTimeout           = 30 * time.Minute
	maxChecks             = 16
	maxConfigFields       = 1000
	maxConfigGroups       = 50
	maxConfigOptions      = 200
	maxConfigItems        = 100
)

var taskNameRE = regexp.MustCompile(`^[a-z][a-z0-9_]{0,47}$`)

// checkManifest reports declarations the host would ignore or reject: setting fields, image fetch rules, tasks and
// attribute labels.
func checkManifest(t *testing.T, m *pluginv1.Manifest) {
	t.Helper()
	checkConfigItems(t, m)
	fields := map[string]*pluginv1.ConfigField{}
	for _, f := range m.GetConfigFields() {
		fields[f.GetKey()] = f
	}
	if n := len(m.GetConfigFields()); n > maxConfigFields {
		t.Errorf("%d config fields; the host keeps the first %d", n, maxConfigFields)
	}
	if n := len(m.GetConfigGroups()); n > maxConfigGroups {
		t.Errorf("%d config groups; the host keeps the first %d", n, maxConfigGroups)
	}
	for _, f := range m.GetConfigFields() {
		checkFieldDefault(t, f)
		if n := len(f.GetOptions()); n > maxConfigOptions {
			t.Errorf("field %q: %d options; the host keeps the first %d", f.GetKey(), n, maxConfigOptions)
		}
		if c := f.GetVisibleWhen(); c != nil {
			ref, ok := fields[c.GetKey()]
			switch {
			case !ok || c.GetKey() == f.GetKey():
				t.Errorf("field %q: visible_when refers to %q, which is not another field", f.GetKey(), c.GetKey())
			case !slices.Contains([]pluginv1.ConfigFieldType{pluginv1.ConfigFieldType_CONFIG_FIELD_TYPE_BOOL,
				pluginv1.ConfigFieldType_CONFIG_FIELD_TYPE_SELECT, pluginv1.ConfigFieldType_CONFIG_FIELD_TYPE_STRING,
				pluginv1.ConfigFieldType_CONFIG_FIELD_TYPE_INT, pluginv1.ConfigFieldType_CONFIG_FIELD_TYPE_MULTISELECT,
				pluginv1.ConfigFieldType_CONFIG_FIELD_TYPE_ORDERED_MULTISELECT}, ref.GetType()):
				t.Errorf("field %q: visible_when refers to %q of type %v; it cannot be compared", f.GetKey(), c.GetKey(), ref.GetType())
			case len(c.GetValues()) == 0:
				t.Errorf("field %q: visible_when has no values, so the field is never shown", f.GetKey())
			}
		}
	}

	if n := len(m.GetImageFetchRules()); n > maxImageFetchRules {
		t.Errorf("%d image_fetch_rules; at most %d", n, maxImageFetchRules)
	}
	for i, r := range m.GetImageFetchRules() {
		if len(r.GetHosts()) == 0 || len(r.GetHosts()) > maxRuleHosts {
			t.Errorf("image_fetch_rules[%d]: %d hosts; want 1 to %d", i, len(r.GetHosts()), maxRuleHosts)
		}
		for _, h := range r.GetHosts() {
			if p, isAddr := parseRuleAddr(h); isAddr {
				switch {
				case !p.IsValid() || h != strings.ToLower(h) || ruleAddrNeverFetched(p):
					t.Errorf("image_fetch_rules[%d]: host %q is not an acceptable IP address or CIDR (loopback, link-local, multicast and unspecified addresses are never fetched)", i, h)
				case ruleAddrPrivate(p) && !slices.Contains(m.GetPermissions(), pluginv1.PermissionLAN):
					t.Errorf("image_fetch_rules[%d]: host %q is a private network address, so the manifest must declare the permission network.lan", i, h)
				}
			} else if !validRuleHost(h) {
				t.Errorf("image_fetch_rules[%d]: host %q is not a lowercase domain name, *.domain, IP address or CIDR", i, h)
			}
		}
		if u, err := url.Parse(r.GetReferer()); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			t.Errorf("image_fetch_rules[%d]: referer %q is not an absolute http(s) URL", i, r.GetReferer())
		}
		if key := r.GetProxyFrom(); key != "" {
			f := fieldByKey(m, key)
			switch {
			case f == nil:
				t.Errorf("image_fetch_rules[%d]: proxy_from %q is not a setting of this plugin", i, key)
			case f.GetType() != pluginv1.ConfigFieldType_CONFIG_FIELD_TYPE_STRING && f.GetType() != pluginv1.ConfigFieldType_CONFIG_FIELD_TYPE_SECRET:
				t.Errorf("image_fetch_rules[%d]: proxy_from %q must be a string or secret setting, got %s", i, key, f.GetType())
			}
		}
	}

	if declares(m, pluginv1.CapabilityPluginTasks) && len(m.GetTasks()) == 0 {
		t.Errorf("the manifest declares %s but lists no tasks", pluginv1.CapabilityPluginTasks)
	}
	seen := map[string]bool{}
	for _, task := range m.GetTasks() {
		name := task.GetName()
		if !taskNameRE.MatchString(name) || seen[name] {
			t.Errorf("task %q: the name must be unique, a lowercase letter then letters, digits or underscores, at most 48", name)
		}
		seen[name] = true
		if strings.TrimSpace(task.GetTitle()) == "" {
			t.Errorf("task %q has no title", name)
		}
		if s := task.GetDefaultSchedule(); s != nil {
			every, cron := s.GetIntervalMinutes(), strings.TrimSpace(s.GetCron())
			switch {
			case (every == 0) == (cron == ""):
				t.Errorf("task %q: set exactly one of interval_minutes and cron", name)
			case every != 0 && (every < minTaskIntervalMinute || every > maxTaskIntervalMinute):
				t.Errorf("task %q: interval_minutes %d is outside %d–%d", name, every, minTaskIntervalMinute, maxTaskIntervalMinute)
			case cron != "" && len(strings.Fields(cron)) != 5:
				t.Errorf("task %q: cron %q needs five fields (minute hour day month weekday)", name, cron)
			}
		}
	}

	if declares(m, pluginv1.CapabilityPluginChecks) && len(m.GetChecks()) == 0 {
		t.Errorf("the manifest declares %s but lists no checks", pluginv1.CapabilityPluginChecks)
	}
	if n := len(m.GetChecks()); n > maxChecks {
		t.Errorf("%d checks; at most %d are used", n, maxChecks)
	}
	groups := map[string]bool{}
	itemGroups := map[string][]*pluginv1.ConfigItem{}
	for _, g := range m.GetConfigGroups() {
		groups[g.GetKey()] = true
		itemGroups[g.GetKey()] = g.GetItems()
	}
	seenChecks := map[string]bool{}
	for _, c := range m.GetChecks() {
		id := c.GetId()
		if !taskNameRE.MatchString(id) || seenChecks[id] {
			t.Errorf("check %q: the id must be unique, a lowercase letter then letters, digits or underscores, at most 48", id)
		}
		seenChecks[id] = true
		if strings.TrimSpace(c.GetLabel()) == "" {
			t.Errorf("check %q has no label", id)
		}
		if g := c.GetGroup(); g != "" && !groups[g] {
			t.Errorf("check %q: group %q is not a declared config group", id, g)
		}
		if c.GetPerItem() && len(itemGroups[c.GetGroup()]) == 0 {
			t.Errorf("check %q: per_item needs a group with items; the host shows it once in the group", id)
		}
	}
	if len(m.GetChecks()) > 0 && !declares(m, pluginv1.CapabilityPluginChecks) {
		t.Errorf("the manifest lists checks but does not declare %s", pluginv1.CapabilityPluginChecks)
	}

	if a := m.GetAuthor(); utf8.RuneCountInString(a) > maxAuthorLen {
		t.Errorf("author %q is longer than %d characters", a, maxAuthorLen)
	}
	if h := m.GetHomepage(); h != "" {
		if u, err := url.Parse(h); err != nil || u.Scheme != "https" || u.Host == "" || len(h) > maxHomepageLen {
			t.Errorf("homepage %q is not an absolute https URL of at most %d characters", h, maxHomepageLen)
		}
	}
	if n := len(m.GetAttributeLabels()); n > maxAttributeLabels {
		t.Errorf("%d attribute_labels; at most %d", n, maxAttributeLabels)
	}
	for _, l := range m.GetAttributeLabels() {
		if !validAttributeKey(l.GetKey()) {
			t.Errorf("attribute label for %q: not a valid attribute key", l.GetKey())
		}
		if label := strings.TrimSpace(l.GetLabel()); label == "" || utf8.RuneCountInString(label) > maxAttributeLabelLen {
			t.Errorf("attribute label for %q: %q must be 1 to %d characters", l.GetKey(), l.GetLabel(), maxAttributeLabelLen)
		}
	}
}

// checkFieldDefault checks the default value's encoding for the newer field types.
func checkFieldDefault(t *testing.T, f *pluginv1.ConfigField) {
	t.Helper()
	d := f.GetDefaultValue()
	switch f.GetType() {
	case pluginv1.ConfigFieldType_CONFIG_FIELD_TYPE_TEXT:
		if len(d) > maxTextBytes {
			t.Errorf("field %q: default is %d bytes; text holds at most %d", f.GetKey(), len(d), maxTextBytes)
		}
	case pluginv1.ConfigFieldType_CONFIG_FIELD_TYPE_STRING_LIST, pluginv1.ConfigFieldType_CONFIG_FIELD_TYPE_ORDERED_MULTISELECT:
		if d == "" {
			return
		}
		var vals []string
		if err := json.Unmarshal([]byte(d), &vals); err != nil {
			t.Errorf("field %q: default %q is not a JSON array of strings", f.GetKey(), d)
			return
		}
		if f.GetType() != pluginv1.ConfigFieldType_CONFIG_FIELD_TYPE_ORDERED_MULTISELECT {
			return
		}
		opts := map[string]bool{}
		for _, o := range f.GetOptions() {
			opts[o.GetValue()] = true
		}
		for _, v := range vals {
			if !opts[v] {
				t.Errorf("field %q: default contains %q, which is not an option", f.GetKey(), v)
			}
		}
	}
}

// parseRuleAddr reads an IP address or CIDR host rule. isAddr is false for anything that is neither (a domain name); a string that
// looks like a CIDR but does not parse comes back as isAddr with an invalid prefix.
func parseRuleAddr(h string) (p netip.Prefix, isAddr bool) {
	if strings.Contains(h, "/") {
		p, err := netip.ParsePrefix(h)
		if err != nil {
			return netip.Prefix{}, true
		}
		return p.Masked(), true
	}
	a, err := netip.ParseAddr(h)
	if err != nil {
		return netip.Prefix{}, false
	}
	a = a.Unmap()
	return netip.PrefixFrom(a, a.BitLen()), true
}

var neverFetchedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("224.0.0.0/4"), netip.MustParsePrefix("::/128"), netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("fe80::/10"), netip.MustParsePrefix("ff00::/8"),
}

// ruleAddrNeverFetched reports whether p overlaps a range the host never fetches from.
func ruleAddrNeverFetched(p netip.Prefix) bool {
	for _, n := range neverFetchedPrefixes {
		if n.Addr().Is4() == p.Addr().Is4() && (n.Contains(p.Addr()) || p.Contains(n.Addr())) {
			return true
		}
	}
	return false
}

// ruleAddrPrivate reports whether p starts in RFC 1918, unique local (fc00::/7) or carrier-grade NAT (100.64.0.0/10) space.
func ruleAddrPrivate(p netip.Prefix) bool {
	return p.Addr().IsPrivate() || netip.MustParsePrefix("100.64.0.0/10").Contains(p.Addr())
}

func validRuleHost(h string) bool {
	h = strings.TrimPrefix(h, "*.")
	if net.ParseIP(h) != nil || !strings.Contains(h, ".") || h != strings.ToLower(h) {
		return false
	}
	for _, l := range strings.Split(h, ".") {
		if l == "" || strings.Trim(l, "abcdefghijklmnopqrstuvwxyz0123456789-") != "" || l[0] == '-' || l[len(l)-1] == '-' {
			return false
		}
	}
	return strings.Trim(h[strings.LastIndex(h, ".")+1:], "0123456789") != ""
}

// runTasks runs the named tasks once each with the host's deadline.
func runTasks(ctx context.Context, t *testing.T, conn *grpc.ClientConn, m *pluginv1.Manifest, names []string) {
	if !declares(m, pluginv1.CapabilityPluginTasks) {
		t.Fatalf("the manifest does not declare %s", pluginv1.CapabilityPluginTasks)
	}
	c := pluginv1.NewTaskRunnerClient(conn)
	for _, name := range names {
		if !slices.ContainsFunc(m.GetTasks(), func(d *pluginv1.TaskDeclaration) bool { return d.GetName() == name }) {
			t.Errorf("task %q is not declared in the manifest", name)
			continue
		}
		resp, err := call(ctx, taskTimeout, func(cctx context.Context) (*pluginv1.RunTaskResponse, error) {
			return c.RunTask(cctx, &pluginv1.RunTaskRequest{Name: name})
		})
		if err != nil {
			t.Errorf("RunTask(%q): %v", name, err)
			continue
		}
		t.Logf("RunTask(%q): message %q, nothing_to_do %v", name, resp.GetMessage(), resp.GetNothingToDo())
	}
}

// checkConfigItems checks ConfigGroup.items and ConfigField.item (PROTOCOL.md §5.2): valid unique ids with a label,
// at most maxConfigItems per group, every field item listed by the field's group, and no item without fields.
func checkConfigItems(t *testing.T, m *pluginv1.Manifest) {
	t.Helper()
	items := map[string]map[string]bool{} // group key → item ids
	for _, g := range m.GetConfigGroups() {
		if n := len(g.GetItems()); n > maxConfigItems {
			t.Errorf("group %q: %d items; at most %d", g.GetKey(), n, maxConfigItems)
		}
		ids := map[string]bool{}
		for _, it := range g.GetItems() {
			if !taskNameRE.MatchString(it.GetId()) || ids[it.GetId()] {
				t.Errorf("group %q: item %q: the id must be unique, a lowercase letter then letters, digits or underscores, at most 48", g.GetKey(), it.GetId())
			}
			ids[it.GetId()] = true
			if strings.TrimSpace(it.GetLabel()) == "" {
				t.Errorf("group %q: item %q has no label", g.GetKey(), it.GetId())
			}
		}
		items[g.GetKey()] = ids
	}
	used := map[string]bool{}
	for _, f := range m.GetConfigFields() {
		it := f.GetItem()
		if it == "" {
			continue
		}
		if !items[f.GetGroup()][it] {
			t.Errorf("field %q: item %q is not an item of its group %q; the host shows the field in the group", f.GetKey(), it, f.GetGroup())
			continue
		}
		used[f.GetGroup()+"\x00"+it] = true
	}
	for _, g := range m.GetConfigGroups() {
		for _, it := range g.GetItems() {
			if !used[g.GetKey()+"\x00"+it.GetId()] {
				t.Errorf("group %q: item %q has no fields", g.GetKey(), it.GetId())
			}
		}
	}
}

// fieldByKey finds a setting by key (fields carry their group; groups do not list fields).
func fieldByKey(m *pluginv1.Manifest, key string) *pluginv1.ConfigField {
	for _, f := range m.GetConfigFields() {
		if f.GetKey() == key {
			return f
		}
	}
	return nil
}
