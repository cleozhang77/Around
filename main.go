package main

import (
	//"cloud.google.com/go/bigtable"
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	//"golang.org/x/text/message/catalog"
	"gopkg.in/olivere/elastic.v3"
	"log"
	"net/http"
	"strconv"

	"github.com/pborman/uuid"

	"cloud.google.com/go/storage"
	"github.com/auth0/go-jwt-middleware"
	"github.com/dgrijalva/jwt-go"
	"github.com/gorilla/mux"
	"io"
)

const (
	DISTANCE    = "2000000km"
	INDEX       = "around"
	TYPE        = "post"
	PROJECT_ID  = "around-220220"
	BT_INSTANCE = "around-post"
	//Needs to update this URL if deploy it to cloud
	ES_URL      = "http://35.231.195.31:9200/"
	BUCKET_NAME = "post-images-220220"
)

type Location struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

type Post struct {
	User     string   `json:"user"`
	Message  string   `json:"message"`
	Location Location `json:"location"`
	Url      string   `json:"url"`
}

var mySigningKey = []byte("secret")

func main() {
	// Create a client
	client, err := elastic.NewClient(elastic.SetURL(ES_URL), elastic.SetSniff(false))
	if err != nil {
		panic(err)
		return
	}

	// Use the IndexExists service to check if a specified index exists
	exists, err := client.IndexExists(INDEX).Do()
	if err != nil {
		panic(err)
	}

	if !exists {
		// Create a new index
		fmt.Println("Create a new index %s", INDEX)
		mapping := `{
			"mappings":{
				"post":{
					"properties":{
						"location":{
							"type":"geo_point"
						}
					}
				}
			}
		}
		`
		_, err := client.CreateIndex(INDEX).Body(mapping).Do()
		if err != nil {
			// Handle error
			panic(err)
		}
	}
	fmt.Println("started-service")
	// Here we are instantiating the gorilla/mux router
	r := mux.NewRouter()

	var jwtMiddleware = jwtmiddleware.New(jwtmiddleware.Options{
		ValidationKeyGetter: func(token *jwt.Token) (interface{}, error) {
			return mySigningKey, nil
		},
		SigningMethod: jwt.SigningMethodHS256,
	})

	r.Handle("/post", jwtMiddleware.Handler(http.HandlerFunc(handlerPost))).Methods("POST", "OPTIONS")
	r.Handle("/search", jwtMiddleware.Handler(http.HandlerFunc(handlerSearch))).Methods("GET", "OPTIONS")
	r.Handle("/login", http.HandlerFunc(loginHandler)).Methods("POST", "OPTIONS")
	r.Handle("/signup", http.HandlerFunc(signupHandler)).Methods("POST", "OPTIONS")

	http.Handle("/", r)
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func handlerPost(w http.ResponseWriter, r *http.Request) {
	if (*r).Method == "OPTIONS" { // handle preflight request
		setupResponse(&w, r)
		fmt.Println(" get into handlerPost preflight options case")
		return
	}

	//w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// other codes
	user := r.Context().Value("user")
	claims := user.(*jwt.Token).Claims
	username := claims.(jwt.MapClaims)["username"]

	// 32 << 20 is the maxMemory param for ParseMultipartForm, equals to 32MB (1MB = 1024 * 1024 bytes = 2^20 bytes)
	// After you call ParseMultipartForm, the file will be saved in the server memory with maxMemory size.
	// If the file size is larger than maxMemory, the rest of the data will be saved in a system temporary file.
	r.ParseMultipartForm(32 << 20)

	// Parse from form data.
	fmt.Printf("Received one post request %s\n", r.FormValue("message"))
	lat, _ := strconv.ParseFloat(r.FormValue("lat"), 64)
	lon, _ := strconv.ParseFloat(r.FormValue("lon"), 64)

	p := &Post{
		User:    username.(string),
		Message: r.FormValue("message"),
		Location: Location{
			Lat: lat,
			Lon: lon,
		},
	}

	id := uuid.New()

	file, _, err := r.FormFile("image")
	if err != nil {
		http.Error(w, "Image is not available", http.StatusInternalServerError)
		fmt.Printf("Image is not available %v.\n", err)
		return
	}

	ctx := context.Background()

	defer file.Close()
	// replace it with your real bucket name.
	_, attrs, err := saveToGCS(ctx, file, BUCKET_NAME, id)
	if err != nil {
		http.Error(w, "GCS is not setup", http.StatusInternalServerError)
		fmt.Printf("GCS is not setup %v\n", err)
		return
	}

	// Update the media link after saving to GCS.
	p.Url = attrs.MediaLink

	// Save to ES
	saveToES(p, id)

	// Save to BitTable, disable bigtable, too expensive!
	/*bt_client, err := bigtable.NewClient(ctx, PROJECT_ID, BT_INSTANCE)
	if err != nil {
		panic(err)
		return
	}

	// open table
	tbl := bt_client.Open("post")
	// Create mutation
	mut := bigtable.NewMutation()

	mut.Set("post", "user", bigtable.Now(), []byte(p.User))
	mut.Set("post", "message", bigtable.Now(), []byte(p.Message))

	mut.Set("location", "lat", bigtable.Now(), []byte(strconv.FormatFloat(p.Location.Lat,'f',-1,64)))
	mut.Set("location", "lon", bigtable.Now(), []byte(strconv.FormatFloat(p.Location.Lon,'f',-1,64)))

	err = tbl.Apply(ctx, "com.google.cloud", mut)
	if (err != nil) {
		panic(err)
		return
	}

	fmt.Printf("Post is saved to BigTable %s/n", p.Message)*/
}
func saveToES(p *Post, id string) {
	// Create a client
	es_client, err := elastic.NewClient(elastic.SetURL(ES_URL), elastic.SetSniff(false))
	if err != nil {
		panic(err)
		return
	}

	// Save it to index
	_, err = es_client.Index().
		Index(INDEX).
		Type(TYPE).
		Id(id).
		BodyJson(p).
		Refresh(true).
		Do()
	if err != nil {
		panic(err)
		return
	}
	fmt.Printf("Post is saved to Index: %s \n", p.Message)
}

func saveToGCS(ctx context.Context, r io.Reader, bucket, name string) (*storage.ObjectHandle, *storage.ObjectAttrs, error) {
	client, err := storage.NewClient(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer client.Close()

	bh := client.Bucket(bucket)
	//Next check if the bucket exists
	if _, err = bh.Attrs(ctx); err != nil {
		return nil, nil, err
	}

	obj := bh.Object(name)
	w := obj.NewWriter(ctx)
	if _, err := io.Copy(w, r); err != nil {
		return nil, nil, err
	}
	if err := w.Close(); err != nil {
		return nil, nil, err
	}

	if err := obj.ACL().Set(ctx, storage.AllUsers, storage.RoleReader); err != nil {
		return nil, nil, err
	}

	attrs, err := obj.Attrs(ctx)
	fmt.Printf("Post is saved to GCS: %s\n", attrs.MediaLink)
	return obj, attrs, err
}

func handlerSearch(w http.ResponseWriter, r *http.Request) {
	if (*r).Method == "OPTIONS" { // handle preflight request
		setupResponse(&w, r)
		fmt.Println(" get into handlerSearch preflight options case")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	fmt.Println("enter handler Search")

	fmt.Println((*r).Method)
	if (*r).Method == "OPTIONS" { // handle preflight request
		//setupResponse(&w, r)
		fmt.Println(" get into options case")
		return
	}

	fmt.Println("Received one request for search")
	lat, _ := strconv.ParseFloat(r.URL.Query().Get("lat"), 64)
	lon, _ := strconv.ParseFloat(r.URL.Query().Get("lon"), 64)
	// range is optional
	ran := DISTANCE
	if val := r.URL.Query().Get("range"); val != "" {
		ran = val + "km"
	}
	//fmt.Fprintf(w, "Search received : lat = %f, lat = %f, range = %s\n", lat, lon, ran)

	// Create a client
	client, err := elastic.NewClient(elastic.SetURL(ES_URL), elastic.SetSniff(false))
	if err != nil {
		fmt.Println("Error when create NewClient for elasticSearch")
		panic(err)
		return
	}

	// Define geo distance query as specified in
	//https://www.elastic.co/guide/en/elasticsearch/reference/5.2/query-dsl-geo-distance-query.html
	q := elastic.NewGeoDistanceQuery("location")
	q = q.Distance(ran).Lat(lat).Lon(lon)

	// Some delay may range from seconds to minutes. So if you don;t get enough results, try it later
	searchResult, err := client.Search().
		Index(INDEX).
		Query(q).
		Pretty(true).
		Do()
	if err != nil {
		// Handel error
		fmt.Println("Error when elasticSearching")
		panic(err)
	}

	// searchResult is of type SearchResult and returns hits, sugestions,
	// and all kinds of other information from Elasticsearch
	fmt.Printf("Query took %d milliseconds\n", searchResult.TookInMillis)
	//TotalHits is another convenience function that works even when something goes wrong
	fmt.Printf("Found a total of %d post in range of range %s\n", searchResult.TotalHits(), ran)

	//Each is a convenience function that iterates over hits in a search result.
	//It makes sure you don't need to check for nil values in the response
	// However, it ignres error in serialization

	var typ Post
	var ps []Post
	for _, item := range searchResult.Each(reflect.TypeOf(typ)) { // instances of
		p := item.(Post) // p = (Post) item
		fmt.Printf("Post by %s: %s at lat %v and lon %v\n", p.User, p.Message, p.Location.Lat, p.Location.Lon)
		//Perform filtering base based on keywords such as web spam etc.
		if (p.Url != "") {
			ps = append(ps, p)
		}
	}
	js, err := json.Marshal(ps)

	if err != nil {
		panic(err)
		return
	}

	w.Write(js)
	/*
	// Return a fake post
	p := &Post {
		User:"1111",
		Message:"一生必去的100个地方",
		Location: Location{
			Lat:lat,
			Lon:lon,
		},
	}

	js, err := json.Marshal(p);
	if err != nil{
		panic(err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(js)*/
}
