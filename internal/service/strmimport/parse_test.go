package strmimport

import (
	"errors"
	"testing"
)

func TestParseContent(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    Ref
		wantErr bool
	}{
		{
			name:    "faststrm",
			content: "http://nas:8090/api/strm?account=alice&pickcode=d18nbyd6w552vx2zs&file_name=Movie.iso\n",
			want:    Ref{Source: SourceFastStrm, Account: "alice", PickCode: "d18nbyd6w552vx2zs", FileName: "Movie.iso"},
		},
		{
			name:    "faststrm legacy",
			content: "http://nas:8090/api/fs/get?account=alice&pickcode=d18nbyd6w552vx2zs\n",
			want:    Ref{Source: SourceFastStrmLegacy, Account: "alice", PickCode: "d18nbyd6w552vx2zs"},
		},
		{
			name:    "p115",
			content: "http://moviepilot:3000/api/v1/plugin/P115StrmHelper/redirect_url?pickcode=d18nbyd6w552vx2zs&file_name=Movie.iso",
			want:    Ref{Source: SourceP115, PickCode: "d18nbyd6w552vx2zs", FileName: "Movie.iso"},
		},
		{
			name:    "p115 share",
			content: "http://moviepilot:3000/api/v1/plugin/P115StrmHelper/redirect_url?share_code=abc&receive_code=xyz&id=123",
			want:    Ref{Source: SourceP115Share, ShareCode: "abc", ReceiveCode: "xyz", FileID: "123"},
		},
		{
			name:    "p115 path route",
			content: "http://moviepilot:3000/api/v1/plugin/P115StrmHelper/redirect_url/电影/Movie.iso",
			want:    Ref{Source: SourceP115Path, RelativePath: "电影/Movie.iso"},
		},
		{
			name:    "litepan play",
			content: "https://pan.example.com/api/strm/play/2/MTIzNDU/t/lpk_strm_x/n/Movie.iso",
			want:    Ref{Source: SourceLitePan, Account: "2", FileID: "12345", FileName: "Movie.iso"},
		},
		{
			name:    "litepan path",
			content: "https://pan.example.com/api/strm/path/2/cm9vdA/Y2luZW1hL01vdmllLmlzbw/t/lpk/n/Movie.iso",
			want:    Ref{Source: SourceLitePanPath, Account: "2", RootID: "cm9vdA", RelativePath: "cinema/Movie.iso", FileName: "Movie.iso"},
		},
		{
			name:    "direct cdn is unknown",
			content: "https://cdn.115.com/foo?t=123",
			want:    Ref{Source: SourceUnknown, RawURL: "https://cdn.115.com/foo?t=123"},
		},
		{
			name:    "invalid faststrm pickcode",
			content: "http://nas:8090/api/strm?account=alice&pickcode=bad",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseContent(tt.content)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseContent: %v", err)
			}
			rawMismatch := tt.want.Source == SourceUnknown && got.RawURL != tt.want.RawURL
			if got.Source != tt.want.Source || got.Account != tt.want.Account ||
				got.PickCode != tt.want.PickCode || got.FileID != tt.want.FileID ||
				got.FileName != tt.want.FileName || got.ShareCode != tt.want.ShareCode ||
				got.ReceiveCode != tt.want.ReceiveCode || got.RootID != tt.want.RootID ||
				got.RelativePath != tt.want.RelativePath || rawMismatch {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestParseContentEmpty(t *testing.T) {
	if _, err := ParseContent(" \n\t\n"); !errors.Is(err, ErrEmptyContent) {
		t.Fatalf("want ErrEmptyContent, got %v", err)
	}
}

func TestRefSupported(t *testing.T) {
	if (Ref{Source: SourceP115Share}).Supported() {
		t.Fatal("share ref should be unsupported")
	}
	if !(Ref{Source: SourceLitePan, FileID: "1"}).Supported() {
		t.Fatal("litepan file id should be supported")
	}
}
