package main

import (
	"encoding/json"
	"fmt"
	"gopkg.in/redis.v4"
	"io/ioutil"
	"net/http"
	"time"
	"regexp"
)

//Main
func main() {

	//First time online, load historical data for bloom
	loadRecords()

	//Announce that we're running
	fmt.Println("AuthTables is running.")
	//Add routes, then open a webserver
	http.HandleFunc("/add", addRequest)
	http.HandleFunc("/check", checkRequest)
	http.ListenAndServe(":8080", nil)

}

func getRecordHashesFromRecord(rec Record) (recordhashes RecordHashes) {

	rh := RecordHashes{
		uid:     []byte(rec.UID),
		uid_mid: []byte(fmt.Sprintf("%s:%s",rec.UID,rec.MID)),
		uid_ip:  []byte(fmt.Sprintf("%s:%s",rec.UID,rec.IP)),
		uid_all: []byte(fmt.Sprintf("%s:%s:%s",rec.UID,rec.IP,rec.MID)),
		ip_mid:  []byte(fmt.Sprintf("%s:%s",rec.IP,rec.MID)),
		mid_ip:  []byte(fmt.Sprintf("%s:%s",rec.MID,rec.IP)),
	}

	return rh
}

func check(rec Record)(b bool) {
	//We've received a request to /check and now
	//we need to see if it's suspicious or not.

	//Create []byte Strings for bloom
	rh := getRecordHashesFromRecord(rec)

	//These is ip:mid and mid:ip, useful for `key`
	//commands hunting for other bad guys. This May
	//be a seperate db, sharded elsewhere in the future.
	//Example: `key 1.1.1.1:*` will reveal new machine ID's
	//seen on this host.
	//This may include evil data, which is why we don't attach to a user.
	writeRecord(rh.ip_mid)
	writeRecord(rh.mid_ip)

	//Do we have it in bloom?
	//if filter.Test([]byte(r.URL.Path[1:])) {
	if filter.Test(rh.uid_all) {
		//We've seen everything about this user before. MachineID, IP, and user.
		fmt.Println("Known user information.")

		//Write Everything.
		defer writeUserRecord(rh)
		return true
	} else if (filter.Test(rh.uid_mid)) || (filter.Test(rh.uid_ip)) {

		fmt.Printf("Either %s or %s is known. Adding both.\n", rec.IP, rec.MID )
		defer writeUserRecord(rh)
		return true

	} else if !(filter.Test(rh.uid)) {

		fmt.Println("New user with no records. Adding records.")
		defer writeUserRecord(rh)
		return true

	} else {

		fmt.Printf("IP: %s and MID: %s are suspicious.\n", rec.IP, rec.MID)
		return false
	}

}

func add(rec Record) (b bool) {

	//JSON record is sent to /add, we add all of it to bloom.
	rh := getRecordHashesFromRecord(rec)
	writeUserRecord(rh)
	return true
}

func isStringSane(s string)(b bool) {

	matched, err := regexp.MatchString("^[A-Za-z0-9.]{0,60}$", s)
	if (err != nil) {
		fmt.Println(err)
	}

	return matched
}

func isRecordSane(r Record)(b bool){

	return (isStringSane(r.MID) && isStringSane(r.IP) && isStringSane(r.UID))

}
func sanitizeError(){
	fmt.Println("Bad data received. Sanitize fields in application before sending to remove this message.")
}

func requestToJson (r *http.Request) (m Record) {
	//Get our body from the request (which should be JSON)
	r.ParseForm()
	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		fmt.Println("error:", err)
	}

	//Cast our JSON body content to prepare for Unmarshal
	client_authdata := []byte(body)

	//Decode some JSON and get it into our Record struct
	var rec Record
	err = json.Unmarshal(client_authdata, &rec)
	if err != nil {
		fmt.Println("error:", err)
	}

	return rec
}

//Main routing handlers
func addRequest(w http.ResponseWriter, r *http.Request) {
	var m Record
	m = requestToJson(r)

	if (isRecordSane(m)){
		fmt.Println("Adding: ", m)

		if (add(m)) {
			fmt.Fprintln(w, "ADD")
		} else {
			fmt.Fprintln(w, "ADD")
		}//Currently we fail open.
	} else {
		sanitizeError()
	}

}

func checkRequest(w http.ResponseWriter, r *http.Request) {
	var m Record
	m = requestToJson(r)

	//Only let sane data through the gate.
	if (isRecordSane(m)) {
		fmt.Printf("Checking %s: ", m.UID)

		if (check(m)) {
			fmt.Fprintln(w, "OK")
		} else {
			fmt.Fprintln(w, "BAD")
		}
	} else {
		//We hit this if nasty JSON data came through. Shouldn't touch bloom or redis.
		//To remove this message, don't let your application send UID, IP, or MID that doesn't match "^[A-Za-z0-9.]{0,60}$"
		sanitizeError()
		fmt.Fprintln(w, "BAD")
	}
}

func writeRecord(key []byte) {

	err := client.Set(string(key), 1, 0).Err()
	if err != nil {
		//(TODO Try to make new connection)
		rebuildConnection()
		fmt.Println("Record not written. Attempting to reconnect...")
		fmt.Println(err)
	}

}

func rebuildConnection() {
	fmt.Println("Attempting to reconnect...")
	client = redis.NewClient(&redis.Options{
		Addr:     c.Host + ":" + c.Port,
		Password: c.Password, // no password set
		DB:       0,          // use default DB
	})
}

func loadRecords() {
	timeTrack(time.Now(), "Loading records")

	var cursor uint64
	var n int
	for {
		var keys []string
		var err error
		keys, cursor, err = client.Scan(cursor, "", 10).Result()
		if err != nil {

			fmt.Println("Could not connect to Database. Error! Continuing without history.")
			break
		}
		n += len(keys)

		for _, element := range keys {
			filter.Add([]byte(element))
		}

		if cursor == 0 {
			break
		}
	}

	fmt.Printf("Loaded %d historical records.\n", n)
}

func writeUserRecord(rh RecordHashes) {

	err := client.MSetNX(string(rh.uid), 1, string(rh.uid_mid), 1, string(rh.uid_ip), 1, string(rh.uid_ip), 1, string(rh.uid_all), 1).Err()
	if err != nil {
		//(TODO Try to make new connection)
		fmt.Println("MSetNX failed")
		fmt.Println(err)
	}

	//Bloom
	filter.Add(rh.uid_mid)
	filter.Add(rh.uid_ip)
	filter.Add(rh.uid)
	filter.Add(rh.uid_all)
}

func timeTrack(start time.Time, name string) {
	elapsed := time.Since(start)
	fmt.Printf("%s took %s\n",name, elapsed.String())
}
