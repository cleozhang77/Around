
package main

import (
	"encoding/json"
	"fmt"
	"github.com/dgrijalva/jwt-go"
	"gopkg.in/olivere/elastic.v3"
	"net/http"
	"reflect"
	"time"
)

const (
	TYPE_USER = "user"
)

type User struct {
	Username string `json:"username"`
	Password string `json:"password"`
}
// checkUser checks whether credential is valid
func checkUser(username, password string) bool {
	es_client, err := elastic.NewClient(elastic.SetURL(ES_URL), elastic.SetSniff(false))
	if err != nil{
		fmt.Printf("ES is not setup %v\n", err)
		return false
	}

	//Search with a term query
	termQuery := elastic.NewTermQuery("username", username)
	queryResult, err := es_client.Search().
		Index(INDEX).
		Query(termQuery).
		Pretty(true).
		Do()
	if err != nil {
		fmt.Printf("ES query failed %v\n", err)
		return false
	}

	var tyu User
	for _,item := range queryResult.Each(reflect.TypeOf(tyu)) {
		u := item.(User)
		return u.Password == password && u.Username == username
	}
	// if no user exist, return false
	return false
}
// add user adds a new user
// Add a new user. Return true if successfully
func addUser(username, password string) bool {
	// In theory, BigTable is a better option for storing user credentials thanES. However
	// since  BT is more expensive then ES so just disable BT and use ElasticSearch
	es_client, err := elastic.NewClient(elastic.SetURL(ES_URL), elastic.SetSniff(false))
	if err != nil {
		fmt.Printf("ES ies not setup %v\n", err)
		return false
	}

	user := &User{
		Username: username,
		Password: password,
	}

	// Search with a term query
	termQuery := elastic.NewTermQuery("username", username)
	queryResult, err := es_client.Search().
		Index(INDEX).
		Query(termQuery).
		Pretty(true).
		Do()
	if err != nil{
		fmt.Printf("ES query failed %v\n", err)
		return false
	}

	if queryResult.TotalHits() > 0 {
		fmt.Printf("User %s has existed, cannot create duplicate user.\n", username)
		return false
	}

	// Save it to index
	_, err = es_client.Index().
		Index(INDEX).
		Type(TYPE_USER).
		Id(username).
		BodyJson(user).
		Refresh(true).
		Do()
	if err != nil {
		fmt.Printf("ES save failed %v\n", err)
		return false
	}
	return true
}

func setupResponse(w *http.ResponseWriter, req *http.Request) {
	(*w).Header().Set("Access-Control-Allow-Origin", "*")
	(*w).Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	(*w).Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization")
}

// if signup is successful, a new session is created
func signupHandler(w http.ResponseWriter, r *http.Request) {
	if (*r).Method == "OPTIONS" { // handle preflight request
		setupResponse(&w, r)
		fmt.Println(" get into options case")
		return
	}

	fmt.Println("Received one signup request")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")

	decoder := json.NewDecoder(r.Body)
	var u User
	if err := decoder.Decode(&u); err != nil {
		panic(err)
		return
	}

	resultMap := map[string]string{"result": "success"}
	j, err := json.Marshal(resultMap)
	if err != nil {
		panic(err)
		return
	}

	if u.Username != "" && u.Password != "" {
		if addUser(u.Username, u.Password) {
			fmt.Println("User added successfully.")
			//w.Write(json.Marshal([]byte("User added successfully"))
			w.Write(j)
		} else {
			fmt.Println("Failed to add a new user.")
			http.Error(w, "Failed to add a new user", http.StatusInternalServerError)
		}
	} else {
		fmt.Println("Empty password or uwername")
		http.Error(w,"Empty password or username", http.StatusInternalServerError)
	}

}

// if login is successful, a new token is created.
func loginHandler(w http.ResponseWriter, r *http.Request){
	if (*r).Method == "OPTIONS" { // handle preflight request
		setupResponse(&w, r)
		fmt.Println(" get into options case")
		return
	}

	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")
	//w.Header().Set("Content-Type", "plain/text")
	fmt.Println("Received one login request")

	decoder := json.NewDecoder(r.Body)
	var u User
	if err := decoder.Decode(&u); err != nil {
		panic(err)
		return
	}

	if checkUser(u.Username, u.Password) {
		token := jwt.New(jwt.SigningMethodHS256)
		claims := token.Claims.(jwt.MapClaims)
		/* Set token claims */
		claims["username"] = u.Username
		claims["exp"] = time.Now().Add(time.Hour * 24).Unix()

		/* Sign the token with our secret */
		tokenString, _ := token.SignedString(mySigningKey)

		/* Finally, write the token to the browser window */
		tokenMap := map[string]string{"token": tokenString}
		tokenJson, err := json.Marshal(tokenMap)
		if err != nil {
			panic(err)
			return
		}

		//w.Write([]byte(tokenString))
		w.Write(tokenJson)
	} else {
		fmt.Println("Invalid password or username.")
		http.Error(w, "Invalid password or username", http.StatusForbidden)
	}

	//w.Header().Set("Content-Type", "text/plain")
	//w.Header().Set("Access-Control-Allow-Origin", "*")
}