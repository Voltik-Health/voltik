package service

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/voltik/voltik/backend/internal/domain"
)

type oneFile struct{ f domain.StoredFile }

func (o oneFile) GetFile(_ context.Context, id uuid.UUID) (domain.StoredFile, error) {
	if id != o.f.ID {
		return domain.StoredFile{}, domain.ErrNotFound
	}
	return o.f, nil
}

func TestSignedFileLinks(t *testing.T) {
	file := domain.StoredFile{ID: uuid.New(), ContentType: "application/pdf", Data: []byte("%PDF")}
	f := NewFiles([]byte("0123456789abcdef0123456789abcdef"), "https://api.example.com/", oneFile{file})
	now := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	f.now = func() time.Time { return now }

	link, expires := f.Link(file.ID)
	if !expires.Equal(now.Add(15*time.Minute)) || !strings.HasPrefix(link, "https://api.example.com/v1/files/"+file.ID.String()+"?expires=") {
		t.Fatalf("link %q expires %v", link, expires)
	}
	sig := link[strings.Index(link, "signature=")+len("signature="):]
	if got, err := f.Open(context.Background(), file.ID, expires.Unix(), sig); err != nil || string(got.Data) != "%PDF" {
		t.Fatalf("open: %v", err)
	}
	for name, open := range map[string]func() error{
		"wrong signature": func() error {
			_, err := f.Open(context.Background(), file.ID, expires.Unix(), "00"+sig[2:])
			return err
		},
		"other file":    func() error { _, err := f.Open(context.Background(), uuid.New(), expires.Unix(), sig); return err },
		"longer expiry": func() error { _, err := f.Open(context.Background(), file.ID, expires.Unix()+3600, sig); return err },
	} {
		if err := open(); !errors.Is(err, domain.ErrLinkExpired) {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
	f.now = func() time.Time { return now.Add(16 * time.Minute) }
	if _, err := f.Open(context.Background(), file.ID, expires.Unix(), sig); !errors.Is(err, domain.ErrLinkExpired) {
		t.Fatalf("after 15 minutes: %v", err)
	}
}

func TestNightscoutSecretHash(t *testing.T) {
	sum := sha1.Sum([]byte("k7qm3x9vfa2wp8re"))
	hexed := hex.EncodeToString(sum[:])
	a, b, c := NightscoutSecretHash("k7qm3x9vfa2wp8re"), NightscoutSecretHash(hexed), NightscoutSecretHash(strings.ToUpper(hexed))
	if !bytes.Equal(a, b) || !bytes.Equal(a, c) {
		t.Fatal("the secret and its SHA-1 (any case) must give the same hash")
	}
	if bytes.Equal(a, NightscoutSecretHash("another-secret")) {
		t.Fatal("different secrets, same hash")
	}
}

func TestCleanImageDropsMetadataAndShrinks(t *testing.T) {
	big := image.NewRGBA(image.Rect(0, 0, 3200, 2400))
	for x := 0; x < 3200; x += 7 {
		big.Set(x, x%2400, color.RGBA{200, 100, 50, 255})
	}
	var pngData bytes.Buffer
	if err := png.Encode(&pngData, big); err != nil {
		t.Fatal(err)
	}
	out, err := cleanImage(pngData.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	img, format, err := image.Decode(bytes.NewReader(out))
	if err != nil || format != "jpeg" {
		t.Fatalf("format %q err %v", format, err)
	}
	if b := img.Bounds(); b.Dx() != 1600 || b.Dy() != 1200 {
		t.Fatalf("size %v, want 1600x1200", b)
	}

	// A JPEG with an EXIF segment (APP1, "Exif") comes out without it.
	var plain bytes.Buffer
	_ = jpeg.Encode(&plain, image.NewRGBA(image.Rect(0, 0, 10, 10)), nil)
	exif := append([]byte{0xFF, 0xE1, 0x00, 0x10}, []byte("Exif\x00\x00GPS-LISBOA")...)
	withExif := append(append([]byte{0xFF, 0xD8}, exif...), plain.Bytes()[2:]...)
	out, err = cleanImage(withExif)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out, []byte("Exif")) || bytes.Contains(out, []byte("GPS-LISBOA")) {
		t.Fatal("the EXIF data is still in the photo")
	}

	if _, err := cleanImage([]byte("%PDF-1.4 not an image")); !errors.Is(err, domain.ErrUnsupportedMediaType) {
		t.Fatalf("a PDF is not a photo: %v", err)
	}
}
