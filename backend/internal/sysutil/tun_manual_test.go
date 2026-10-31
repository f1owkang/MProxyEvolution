package sysutil

import (
	"reflect"
	"testing"
)

func TestParsePackagesUIDs(t *testing.T) {
	content := `com.example.a 10123 0 /data/user/0/com.example.a default:targetSdkVersion=34
com.example.b 10124 0 /data/user/0/com.example.b default:targetSdkVersion=34
com.example.c 10125 10 /data/user/10/com.example.c default:targetSdkVersion=34
`
	cases := []struct {
		name     string
		packages []string
		want     []string
	}{
		{"包名映射", []string{"com.example.a", "com.example.b"}, []string{"10123", "10124"}},
		{"数字 UID 直通", []string{"10123"}, []string{"10123"}},
		{"未知包名忽略", []string{"com.example.a", "com.missing"}, []string{"10123"}},
		{"空输入", nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parsePackagesUIDs(content, tc.packages); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("parsePackagesUIDs()=%v，期望 %v", got, tc.want)
			}
		})
	}
}
