// Get the input field
let input = document.getElementById("channel");

// Execute a function when the user presses a key on the keyboard
input.addEventListener("keypress", function(event) {
    // If the user presses the "Enter" key on the keyboard
    if (event.key === "Enter") {
        // Cancel the default action, if needed
        event.preventDefault();
        // Trigger the button element with a click
        generateID();
    }
});

function request(url, clientsURlstring) {
    fetch(window.location.pathname.replace("admin", "") + url + window.location.search + clientsURlstring)
        .then(function(response) {
            return response.json();
        })
        .then(function(jsonResponse) {
            if ("error_code" in jsonResponse) {
                alert("The following error happened: "+ jsonResponse["error_code"] + " - " + jsonResponse["description"] + "\nContact the administrator.");
            }
            else {
                location.reload();
            }
        });
}

function deleteAllIDs() {
    let table = document.getElementById("table")
    let clientsURlstring = "";
    for (let i = 1; i < table.rows.length; i++) {
        clientsURlstring = clientsURlstring  + "&clients=" + table.rows[i].cells[0].innerText;
    }
    request("removeClients", clientsURlstring);
}

function deleteUniqueIDs() {
    let table = document.getElementById("table")
    let clientsURlstring = "";
    for (let i = 1; i < table.rows.length; i++) {
        if (table.rows[i].cells[3].children[0].checked === true) {
            clientsURlstring = clientsURlstring  + "&clients=" + table.rows[i].cells[0].innerText;
        }
    }
    if (clientsURlstring === "") {
        alert("No clients selected!");
        return;
    }
    request("removeClients", clientsURlstring);
}

function generateID() {
    let button = document.getElementById("channel")
    request("addClient", "&twitch_channel=" + button.value);
}
