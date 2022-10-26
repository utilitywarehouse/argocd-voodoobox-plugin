package main

import "testing"

func Test_hasSSHRemoteBaseURL(t *testing.T) {
	type args struct {
		cwd string
	}
	tests := []struct {
		name    string
		args    args
		want    bool
		wantErr bool
	}{
		{"with remote base", args{"./testData/app-with-remote-base"}, true, false},
		{"without remote base", args{"./testData/app-with-secrets"}, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			files, err := findKustomizeFiles(tt.args.cwd)
			if err != nil {
				t.Fatal(err)
			}
			got, err := hasSSHRemoteBaseURL(files)
			if (err != nil) != tt.wantErr {
				t.Errorf("hasSSHRemoteBaseURL() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("hasSSHRemoteBaseURL() = %v, want %v", got, tt.want)
			}
		})
	}
}
